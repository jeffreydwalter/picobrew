import { useState } from 'react'
import {
  ArrowUpRight,
  Copy,
  Settings2,
  RefreshCw,
  Pencil,
  Trash2,
  SlidersHorizontal,
} from 'lucide-react'
import { brewerOutputsIdle, isAccessory, preferenceFields, supports } from '../lib/capabilities'
import { api, copyText, errorMessage } from '../lib/api'
import type { Device } from '../types'
import { MachineEvents } from './MachineEvents'
import { Button, Confirm, Modal } from './ui'

export function MachineManagement({
  device,
  testing,
  test,
  edit,
  preferences,
  preferencesDisabled,
  remove,
}: {
  device: Device
  testing: boolean
  test: () => void
  edit: () => void
  preferences: () => void
  preferencesDisabled?: boolean
  remove: () => void
}) {
  const [open, setOpen] = useState(false)
  const [restart, setRestart] = useState(false)
  const [working, setWorking] = useState(false)
  const [notice, setNotice] = useState('')
  async function accessMachine(action: 'copy' | 'open') {
    setWorking(true)
    setNotice('')
    try {
      if (action === 'copy') {
        const { api_key } = await api<{ api_key: string }>(`/devices/${device.id}/key`, {
          method: 'POST',
        })
        await copyText(api_key)
        setNotice('Device API key copied.')
      } else {
        const { url } = await api<{ url: string }>(`/devices/${device.id}/website`, {
          method: 'POST',
        })
        window.location.assign(url)
      }
    } catch (e) {
      setNotice(errorMessage(e))
    } finally {
      setWorking(false)
    }
  }
  const restartReady = device.status?.phase === 4 && brewerOutputsIdle(device, 0)
  async function restartBrewer() {
    if (working) return
    if (!device.capabilities?.commands?.includes('restart') || !restartReady) {
      setNotice('Restart is unavailable. Check that the brewer is connected and idle.')
      setRestart(false)
      setOpen(true)
      return
    }
    setWorking(true)
    try {
      await api(`/devices/${device.id}/commands`, {
        method: 'POST',
        command: true,
        body: { command: 'restart', confirm: true },
      })
      setNotice('Restart acknowledged. The Hub will reconnect when the brewer is ready.')
    } catch (error) {
      setNotice(errorMessage(error))
    } finally {
      setWorking(false)
      setRestart(false)
      setOpen(true)
    }
  }
  function choose(action: () => void) {
    setOpen(false)
    action()
  }
  return (
    <>
      <Button variant="secondary" aria-haspopup="dialog" onClick={() => setOpen(true)}>
        <Settings2 size={16} />
        Manage machine
      </Button>
      {open && (
        <Modal
          title="Machine management"
          onClose={() => setOpen(false)}
          actions={
            <Button variant="danger" className="management-button" onClick={() => choose(remove)}>
              <Trash2 size={15} />
              Remove machine
            </Button>
          }
        >
          <h3>{device.name}</h3>
          {device.base_url && <p className="management-address">{device.base_url}</p>}
          <div className="inline-actions management-actions">
            <Button
              variant="secondary"
              disabled={testing || !device.enabled}
              onClick={() => choose(test)}
            >
              <RefreshCw size={16} />
              Test connection
            </Button>
            <Button variant="secondary" onClick={() => choose(edit)}>
              <Pencil size={16} />
              Edit connection
            </Button>
            {(preferenceFields(device).length > 0 ||
              supports(device.capabilities, 'telemetry_forwarding')) && (
              <Button
                variant="secondary"
                disabled={preferencesDisabled}
                onClick={() => choose(preferences)}
              >
                <SlidersHorizontal size={16} />
                {isAccessory(device) ? 'Device preferences' : 'Brewer preferences'}
              </Button>
            )}
            {device.capabilities?.commands?.includes('restart') && (
              <Button
                variant="secondary"
                disabled={working || testing || !restartReady}
                onClick={() => choose(() => setRestart(true))}
              >
                Restart brewer
              </Button>
            )}
          </div>
          <MachineEvents device={device} />
          {device.adapter === 'pico-standalone' && device.key_configured && (
            <section className="management-key" aria-label="Device API key">
              <div className="management-key-heading">
                <h4>Device API key</h4>
                <Button
                  variant="secondary"
                  className="management-button"
                  disabled={working}
                  onClick={() => void accessMachine('copy')}
                >
                  <Copy size={15} />
                  Copy key
                </Button>
              </div>
              <p>
                The Hub uses this key to control the machine. It also signs you in to the machine’s
                local website.
              </p>
              {supports(device.capabilities, 'pairing_v1') && (
                <button
                  className="text-button"
                  disabled={working || !device.online}
                  onClick={() => void accessMachine('open')}
                >
                  Open machine website
                  <ArrowUpRight size={15} />
                </button>
              )}
            </section>
          )}
          {notice && (
            <p className="management-notice" role="status">
              {notice}
            </p>
          )}
        </Modal>
      )}
      {restart && (
        <Confirm
          title="Restart brewer?"
          description={`Restart ${device.name} and reconnect after it boots.`}
          label="Restart brewer"
          busy={working}
          onConfirm={() => void restartBrewer()}
          onClose={() => {
            setRestart(false)
            setOpen(true)
          }}
        />
      )}
    </>
  )
}
