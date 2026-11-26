import { useState, type FormEvent } from 'react'
import { Download, RefreshCw } from 'lucide-react'
import { useHub, useResource } from '../context'
import { api, download, errorMessage } from '../lib/api'
import type { FirmwareCandidate, FirmwarePackage, MachineFirmware, FirmwareUpdates } from '../types'
import { Button, ErrorBanner, Field, Loading, Modal } from './ui'
import { StockRestoreGuide } from './StockRestoreGuide'
import { usbModels } from './USBInstructions'

function firmwareModelName(model: string) {
  return model === 'pico-s'
    ? 'Pico S'
    : model === 'pico-pro'
      ? 'Pico Pro'
      : model === 'pico-c'
        ? 'Pico C'
        : {
            'z-series': 'Pico Z',
            picoferm: 'PicoFerm',
            picostill: 'PicoStill',
          }[model] ||
          model ||
          'Not identified'
}

const packageSources = {
  catalog: 'Release catalog',
  upload: 'Local upload',
  saved: 'Saved package',
}

function matchesWirelessPackage(item: FirmwareCandidate, machine: MachineFirmware | null) {
  if (item.ota_protocol) {
    return (
      ((item.ota_protocol === 'esp32-factory-v1' &&
        (item.model === 'picostill' || item.model === 'z-series')) ||
        (item.ota_protocol === 'esp8266-ab-v1' && item.model === 'picoferm')) &&
      item.model === machine?.model &&
      item.model_family === item.model &&
      !item.display_variant &&
      !item.hub_ota_protocol
    )
  }
  return (
    item.model === machine?.model &&
    item.hub_ota_protocol === 1 &&
    (item.model === 'pico-c'
      ? item.model_family === 'pico-c' &&
        (item.display_variant === 'ssd1325' || item.display_variant === 'ssd1309') &&
        item.display_variant === machine.display_variant
      : (item.model === 'pico-s' || item.model === 'pico-pro') &&
        item.model_family === 'pico-s-pro' &&
        !item.display_variant)
  )
}

export function MachineFirmwareDialog({
  deviceID,
  onClose,
}: {
  deviceID: string
  onClose: () => void
}) {
  const hub = useHub()
  const firmware = useResource<MachineFirmware>(
    `/devices/${encodeURIComponent(deviceID)}/firmware`,
    4000
  )
  const [display, setDisplay] = useState('')
  const [checked, setChecked] = useState(false)
  const [saving, setSaving] = useState(false)
  const [verificationError, setVerificationError] = useState('')
  const updates = useResource<FirmwareUpdates>('/firmware/updates', 60000)
  const [chosen, setChosen] = useState('')
  const [activity, setActivity] = useState('')
  const [restoreUID, setRestoreUID] = useState('')
  const [wirelessReview, setWirelessReview] = useState<{ uid: string; reviewed: boolean } | null>(
    null
  )
  const [restoreAcknowledged, setRestoreAcknowledged] = useState(false)
  const machine = firmware.data
  const restoreTarget = machine?.stock_restore_guide
    ? machine.usb_restore_target || { model: machine.model, device_uid: machine.device_uid }
    : undefined
  const eligibleRestoreUID =
    restoreTarget && usbModels.includes(restoreTarget.model) ? restoreTarget.device_uid : undefined
  const pendingStock = machine?.last_stock_restore
  const resumeStock =
    !!pendingStock?.stage && ['running', 'interrupted'].includes(pendingStock.state)
  const wirelessUID = machine?.stock_restore_wireless
    ? resumeStock
      ? pendingStock?.device_uid
      : machine.device_uid
    : undefined
  async function confirmStockStartup() {
    if (!pendingStock || !machine?.can_confirm_stock_startup || !!activity) return
    setActivity('Saving startup confirmation…')
    setVerificationError('')
    try {
      await api(`/devices/${encodeURIComponent(deviceID)}/firmware/stock-started`, {
        method: 'POST',
        body: { device_uid: pendingStock.device_uid, started: pendingStock.started },
      })
      firmware.reload()
    } catch (error) {
      setVerificationError(errorMessage(error))
    } finally {
      setActivity('')
    }
  }
  async function restoreWireless() {
    if (
      !wirelessUID ||
      wirelessUID !== wirelessReview?.uid ||
      !wirelessReview.reviewed ||
      !restoreAcknowledged ||
      activity
    )
      return
    setActivity('Requesting stock restoration…')
    setVerificationError('')
    try {
      await api(`/devices/${encodeURIComponent(deviceID)}/firmware/restore-stock`, {
        method: 'POST',
        body: { device_uid: wirelessUID, acknowledge_loss_of_hub: true, confirm_restore: true },
      })
      setWirelessReview(null)
      setRestoreAcknowledged(false)
      firmware.reload()
      hub.notify('Stock restoration requested.')
    } catch (error) {
      setVerificationError(errorMessage(error))
    } finally {
      setActivity('')
    }
  }
  const items = new Map<string, FirmwareCandidate & { source: keyof typeof packageSources }>()
  for (const item of updates.data?.releases || []) {
    if (
      ((item.kind === 'standalone' && !item.ota_protocol) ||
        (item.kind === 'reference' && item.ota_protocol === 'esp32-factory-v1') ||
        (item.kind === 'picoferm-ab' && item.ota_protocol === 'esp8266-ab-v1')) &&
      matchesWirelessPackage(item, machine)
    ) {
      items.set(item.archive_sha256, { ...item, source: 'catalog' })
    }
  }
  for (const item of machine?.packages || []) {
    if (!items.has(item.id) && matchesWirelessPackage(item, machine)) {
      items.set(item.id, {
        ...item,
        source: item.provenance?.source === 'upload' ? 'upload' : 'saved',
      })
    }
  }
  const versionCounts = new Map<string, number>()
  for (const { firmware_version } of items.values()) {
    versionCounts.set(firmware_version, (versionCounts.get(firmware_version) || 0) + 1)
  }
  const versions = [...items].sort(
    ([, a], [, b]) =>
      Number(a.firmware_version.startsWith('preview')) -
        Number(b.firmware_version.startsWith('preview')) ||
      b.firmware_version.localeCompare(a.firmware_version, 'en', { numeric: true })
  )
  const id = items.has(chosen)
    ? chosen
    : machine?.retry_package_id && items.has(machine.retry_package_id)
      ? machine.retry_package_id
      : versions[0]?.[0]
  const selected = id ? items.get(id) : undefined
  const resuming = !!id && machine?.retry_package_id === id
  const supportsInstall =
    machine?.install_supported &&
    !!selected &&
    (machine.model !== 'pico-c' || machine.verified_display_variant === selected.display_variant) &&
    (!machine.retry_package_id || resuming)
  const alreadyInstalled =
    !!selected && selected.firmware_version === machine?.current_version && !resuming
  const installLabel = resuming
    ? 'Resume update'
    : alreadyInstalled
      ? 'Already installed'
      : 'Install'
  async function checkUpdates() {
    setActivity('Checking…')
    setVerificationError('')
    try {
      updates.setData(await api<FirmwareUpdates>('/firmware/updates', { method: 'POST' }))
      firmware.reload()
    } catch (error) {
      setVerificationError(errorMessage(error))
    } finally {
      setActivity('')
    }
  }
  async function useVersion(install: boolean) {
    if (!id || !selected || !machine || (install && (!supportsInstall || alreadyInstalled))) return
    setVerificationError('')
    setActivity(install ? 'Preparing update…' : 'Downloading…')
    try {
      if (selected.source === 'catalog') {
        const saved = await api<FirmwarePackage>(`/firmware/releases/${id}/stage`, {
          method: 'POST',
        })
        if (saved.id !== id)
          throw new Error('The staged package does not match the selected release.')
      }
      if (install) {
        await api(`/devices/${encodeURIComponent(deviceID)}/firmware/install`, {
          method: 'POST',
          body: { package_id: id, device_uid: machine.device_uid, confirm: true },
        })
        hub.notify('Firmware update started.')
      } else {
        await download(
          `/firmware/packages/${id}/download`,
          `${selected.model}-${selected.display_variant ? selected.display_variant + '-' : ''}${selected.firmware_version}.zip`
        )
      }
      firmware.reload()
    } catch (error) {
      setVerificationError(errorMessage(error))
    } finally {
      setActivity('')
    }
  }
  async function verifyController(event: FormEvent) {
    event.preventDefault()
    if (!checked || !display || saving || !firmware.data?.device_uid) return
    setSaving(true)
    setVerificationError('')
    try {
      await api(`/devices/${encodeURIComponent(deviceID)}/firmware/hardware`, {
        method: 'PUT',
        body: { device_uid: firmware.data.device_uid, display_variant: display },
      })
      setChecked(false)
      setDisplay('')
      firmware.reload()
      hub.notify('Verified display controller saved.')
    } catch (error) {
      setVerificationError(errorMessage(error))
    } finally {
      setSaving(false)
    }
  }
  if (wirelessUID && wirelessReview?.uid === wirelessUID) {
    const close = () => {
      setWirelessReview(null)
      setRestoreAcknowledged(false)
      setVerificationError('')
    }
    return (
      <Modal
        title={
          wirelessReview.reviewed
            ? 'Confirm wireless stock restoration'
            : 'Restore original PicoBrew firmware?'
        }
        onClose={close}
      >
        <p>
          {wirelessReview.reviewed
            ? 'The Hub will verify both original images, write them over Wi-Fi, and restart this brewer. Keep the brewer powered on.'
            : 'Returning to original firmware removes standalone controls, the local website, and Hub wireless updates. Returning to custom firmware requires USB.'}
        </p>
        {!wirelessReview.reviewed && (
          <label className="check-label">
            <input
              type="checkbox"
              checked={restoreAcknowledged}
              onChange={(e) => setRestoreAcknowledged(e.target.checked)}
            />{' '}
            I understand that Hub controls and wireless updates will end.
          </label>
        )}
        <ErrorBanner message={verificationError} />
        <div className="modal-actions">
          <Button variant="secondary" disabled={!!activity} onClick={close}>
            Cancel
          </Button>
          {wirelessReview.reviewed ? (
            <Button variant="danger" disabled={!!activity} onClick={() => void restoreWireless()}>
              {resumeStock ? 'Resume stock restoration' : 'Restore and restart brewer'}
            </Button>
          ) : (
            <Button
              disabled={!restoreAcknowledged}
              onClick={() => setWirelessReview({ uid: wirelessUID, reviewed: true })}
            >
              Review restoration
            </Button>
          )}
        </div>
      </Modal>
    )
  }
  if (machine && restoreTarget && eligibleRestoreUID && restoreUID === eligibleRestoreUID) {
    return (
      <StockRestoreGuide
        model={restoreTarget.model}
        deviceID={deviceID}
        display={machine.verified_display_variant}
        onClose={() => setRestoreUID('')}
      />
    )
  }
  return (
    <Modal title="Machine firmware" onClose={onClose} wide>
      <ErrorBanner message={firmware.error} retry={firmware.reload} />
      {firmware.loading ? (
        <Loading label="Loading machine firmware…" />
      ) : (
        firmware.data && (
          <div className="machine-firmware">
            <div className="firmware-current">
              <strong>
                {hub.devices?.find((d) => d.id === deviceID)?.name ||
                  firmwareModelName(firmware.data.model)}
              </strong>
              <span>Current version: {firmware.data.current_version || 'Not reported'}</span>
            </div>
            <p role="status">{firmware.data.reason}</p>
            {firmware.data.last_install && (
              <p role="status">
                Last update ({firmware.data.last_install.state}):{' '}
                {firmware.data.last_install.message}
              </p>
            )}
            <ErrorBanner message={verificationError} />
            {firmware.data.model === 'pico-c' && (
              <details open={firmware.data.hardware_verification_required}>
                <summary>Display controller verification</summary>
                <p className="help-text">
                  Firmware reports{' '}
                  {firmware.data.display_variant?.toUpperCase() || 'an unknown controller'}. This is
                  a build setting, not hardware detection. Check the physical controller or a
                  documented hardware identification before marking its firmware packages as
                  matching. The Hub saves this check for this machine.
                </p>
                {firmware.data.verified_display_variant && (
                  <p>Verified hardware: {firmware.data.verified_display_variant.toUpperCase()}</p>
                )}
                <form onSubmit={verifyController}>
                  <Field label="Verified display controller">
                    <select
                      value={display}
                      disabled={saving}
                      onChange={(event) => {
                        setDisplay(event.target.value)
                        setChecked(false)
                      }}
                    >
                      <option value="">Choose the controller you checked</option>
                      <option value="ssd1325">SSD1325</option>
                      <option value="ssd1309">SSD1309</option>
                    </select>
                  </Field>
                  <label className="check-label">
                    <input
                      type="checkbox"
                      checked={checked}
                      disabled={saving || !display}
                      onChange={(event) => setChecked(event.target.checked)}
                    />{' '}
                    I checked this machine's display controller independently of its firmware
                    report.
                  </label>
                  <Button
                    type="submit"
                    disabled={saving || !checked || !display || !firmware.data.device_uid}
                  >
                    {saving ? 'Saving…' : 'Save verified controller'}
                  </Button>
                </form>
              </details>
            )}
            <Field label="Firmware version">
              <select
                value={id || ''}
                disabled={!versions.length || !!activity}
                onChange={(e) => setChosen(e.target.value)}
              >
                {!versions.length && (
                  <option value="">No compatible wireless firmware available</option>
                )}
                {versions.map(([key, v]) => (
                  <option key={key} value={key}>
                    {v.firmware_version}
                    {v.display_variant ? ' · ' + v.display_variant.toUpperCase() : ''}
                    {v.firmware_version === machine?.current_version ? ' · Current version' : ''}
                    {(versionCounts.get(v.firmware_version) ?? 0) > 1
                      ? ` · ${packageSources[v.source]} (${key.slice(0, 12)})`
                      : ''}
                  </option>
                ))}
              </select>
            </Field>
            <div className="inline-actions">
              {supportsInstall && (
                <Button
                  disabled={alreadyInstalled || !!activity || saving}
                  onClick={() => void useVersion(true)}
                >
                  {installLabel}
                </Button>
              )}
              <Button
                variant="secondary"
                disabled={!selected || !!activity}
                onClick={() => void useVersion(false)}
              >
                <Download size={16} />
                Download
              </Button>
              <Button variant="secondary" disabled={!!activity} onClick={() => void checkUpdates()}>
                <RefreshCw size={16} />
                Check for Updates
              </Button>
            </div>
            {activity && <p role="status">{activity}</p>}
            {updates.data?.checked && (
              <p className="help-text">
                Last checked {new Date(updates.data.checked).toLocaleString()}
              </p>
            )}
            <ErrorBanner message={updates.error} />
            {pendingStock && <p role="status">Stock restoration: {pendingStock.message}</p>}
            {machine?.can_confirm_stock_startup && (
              <Button
                variant="secondary"
                disabled={!!activity || saving}
                onClick={() => void confirmStockStartup()}
              >
                I see the original PicoBrew startup screen
              </Button>
            )}
            {wirelessUID && (
              <Button
                variant="secondary"
                disabled={!!activity || saving}
                onClick={() => {
                  setRestoreAcknowledged(false)
                  setVerificationError('')
                  setWirelessReview({ uid: wirelessUID, reviewed: false })
                }}
              >
                {resumeStock
                  ? 'Resume wireless stock restoration'
                  : 'Restore original firmware over Wi-Fi'}
              </Button>
            )}
            {machine?.stock_restore_upgrade && (
              <p className="help-text">
                Update the custom firmware to a version with wireless stock restoration to return
                this brewer to stock over Wi-Fi.
              </p>
            )}
            {eligibleRestoreUID && (
              <section className="stock-restore-entry">
                <Button
                  variant="secondary"
                  disabled={!!activity || saving}
                  onClick={() => setRestoreUID(eligibleRestoreUID)}
                >
                  Restore original firmware via USB
                </Button>
              </section>
            )}
          </div>
        )
      )}
    </Modal>
  )
}
