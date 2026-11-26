import { useState } from 'react'
import { Link2, Pencil } from 'lucide-react'
import { useHub, useResource } from '../context'
import type { Device } from '../types'
import { api, errorMessage } from '../lib/api'
import { temperature } from '../lib/utils'
import { TransferLoadingSheet } from './RecipeLoadingSheet'
import { StockZTemperatureLimits, type StockZTemperatureLimit } from './StockZTemperatureLimits'
import type { Ingredient } from '../types'
import { Button, ErrorBanner, Field, Modal } from './ui'

export interface BrewGroup {
  id: string
  name: string
  device_ids: string[]
  revision: number
}
type Invite = {
  id: string
  group_name: string
  source_name: string
  target_device_id: string
  action: string
  program_name?: string
  water_l?: number
  temperature_f?: number
  intensity?: number
  time_seconds?: number
  selected_abv?: number
  selected_ibu?: number
  recipe_name?: string
  state: string
  message?: string
  recipe_id?: string
  recipe_revision?: number
  stock_z_review?: {
    temperature_limits?: StockZTemperatureLimit[]
    batch_liters?: number
    ingredients?: Ingredient[]
    hop_slots_by_minute?: Record<number, number>
  }
  stock_z_name?: string
  stock_z_url?: string
  menu_serial?: string
  expires_at: string
}

type Member = { id: string; name: string; online?: boolean }

const inviteStates: Record<string, string> = {
  waiting: 'Waiting for brewer',
  offering: 'Delivering invitation',
  pending: 'Awaiting response',
  responding: 'Confirming response',
  accepted: 'Accepted',
  declined: 'Declined',
  withdrawn: 'Withdrawn',
  expired: 'Expired',
  unavailable: 'Unavailable',
  unconfirmed: 'Delivery unconfirmed',
}

function brewCapable(model: string) {
  return ['pico-s', 'pico-pro', 'pico-c', 'z-series', 'zymatic'].includes(model)
}

export function BrewGroupManager({ devices }: { devices: Device[] }) {
  const { notify } = useHub()
  const groups = useResource<{ groups: BrewGroup[]; stock_candidates: Member[] }>(
    '/brew-groups',
    30000
  )
  const brewers: Member[] = [
    ...devices.filter((d) => d.adapter === 'pico-standalone' && brewCapable(d.model)),
    ...(groups.data?.stock_candidates || []),
  ]
  const [editing, edit] = useState<BrewGroup | 'new' | null>(null)
  if (!brewers.length && !groups.data?.groups?.length) return null
  return (
    <>
      <div className="brew-group-bar">
        {groups.data?.groups?.map((g) => (
          <Button
            key={g.id}
            variant="secondary"
            onClick={() => edit(g)}
            aria-label={`Manage ${g.name}`}
          >
            <Link2 size={16} /> {g.name} · {g.device_ids.length}
            <Pencil size={14} />
          </Button>
        ))}
        {brewers.length >= 2 && (
          <Button variant="secondary" onClick={() => edit('new')}>
            <Link2 size={16} /> Group machines
          </Button>
        )}
      </div>
      <ErrorBanner message={groups.error} retry={groups.reload} />
      {editing && (
        <BrewGroupEditor
          devices={brewers}
          groups={groups.data?.groups || []}
          group={editing === 'new' ? null : editing}
          onClose={() => edit(null)}
          onSaved={(message) => {
            edit(null)
            groups.reload()
            notify(message)
          }}
        />
      )}
    </>
  )
}

function BrewGroupEditor({
  devices,
  groups,
  group,
  onClose,
  onSaved,
}: {
  devices: Member[]
  groups: BrewGroup[]
  group: BrewGroup | null
  onClose: () => void
  onSaved: (message: string) => void
}) {
  const [name, setName] = useState(group?.name || '')
  const [members, select] = useState(group?.device_ids || [])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [removing, setRemoving] = useState(false)
  async function save() {
    setBusy(true)
    setError('')
    try {
      if (group && removing) {
        await api(`/brew-groups/${group.id}?revision=${group.revision}`, { method: 'DELETE' })
        onSaved('Machines ungrouped.')
      } else {
        await api(group ? `/brew-groups/${group.id}` : '/brew-groups', {
          method: group ? 'PUT' : 'POST',
          body: { name: name.trim(), device_ids: members, revision: group?.revision || 0 },
        })
        onSaved('Brew group saved.')
      }
    } catch (e) {
      setError(errorMessage(e))
      setBusy(false)
    }
  }
  const close = () => {
    if (!busy) onClose()
  }
  return (
    <Modal
      title={removing ? `Ungroup ${group?.name}?` : group ? 'Edit brew group' : 'Group machines'}
      onClose={close}
      actions={
        <>
          {group && !removing && (
            <Button variant="ghost" disabled={busy} onClick={() => setRemoving(true)}>
              Ungroup machines
            </Button>
          )}
          <Button
            variant="secondary"
            disabled={busy}
            onClick={() => (removing ? setRemoving(false) : close())}
          >
            Cancel
          </Button>
          <Button
            variant={removing ? 'danger' : 'primary'}
            disabled={busy || (!removing && (!name.trim() || members.length < 2))}
            onClick={() => void save()}
          >
            {removing ? 'Ungroup' : group ? 'Save group' : 'Group'}
          </Button>
        </>
      }
    >
      <ErrorBanner message={error} />
      {removing ? (
        <p>
          These machines will stop receiving this group’s brew invitations. Current brews and
          journal entries are kept.
        </p>
      ) : (
        <>
          <Field label="Group name">
            <input
              value={name}
              maxLength={80}
              placeholder="e.g. Z stack"
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <fieldset className="brew-group-members">
            <legend>Machines</legend>
            {[
              ...devices,
              ...(group?.device_ids || [])
                .filter((id) => !devices.some((d) => d.id === id))
                .map((id) => ({ id, name: id, online: false })),
            ].map((d) => {
              const other = groups.find((g) => g.id !== group?.id && g.device_ids.includes(d.id))
              return (
                <label key={d.id} className="checkbox-row">
                  <input
                    type="checkbox"
                    checked={members.includes(d.id)}
                    disabled={!!other || (members.length >= 16 && !members.includes(d.id))}
                    onChange={(e) => {
                      const checked = e.currentTarget.checked
                      select((current) =>
                        checked ? [...current, d.id] : current.filter((id) => id !== d.id)
                      )
                    }}
                  />
                  <span>
                    {d.name}
                    {other
                      ? ` · ${other.name}`
                      : d.id.startsWith('stock-z:')
                        ? ' · Program Maker'
                        : d.id.startsWith('stock-zymatic:')
                          ? ' · Recipe menu'
                          : d.online === false
                            ? ' · Offline'
                            : ''}
                  </span>
                </label>
              )
            })}
          </fieldset>
          <p>
            When a brewer starts a compatible program, the Hub invites its group. Accept on each
            machine you want to join. A Zymatic using its recipe menu starts when you select the
            recipe on the dial.
          </p>
        </>
      )}
    </Modal>
  )
}

export function BrewInvitations({ devices, deviceID }: { devices: Device[]; deviceID?: string }) {
  const hasBrewer = devices.some((d) => d.adapter === 'pico-standalone' && brewCapable(d.model))
  const fleet = useResource<{ invites: Invite[] }>(!deviceID ? '/brew-invites' : null, 4000)
  const machine = useResource<{ invite: Invite | null }>(
    hasBrewer && deviceID ? `/devices/${deviceID}/brew-invite` : null,
    4000
  )
  const invites = deviceID
    ? machine.data?.invite
      ? [machine.data.invite]
      : []
    : fleet.data?.invites || []
  const [error, setError] = useState('')
  const [pending, setPending] = useState('')
  const { notify, unit, recipes } = useHub()
  const [replacement, setReplacement] = useState<Invite | null>(null)
  const [confirmIdle, setConfirmIdle] = useState(false)
  async function respond(invite: Invite, accept: boolean, confirmReplace = false) {
    setError('')
    setPending(invite.id + invite.target_device_id)
    try {
      const result = await api<Invite>(
        `/devices/${invite.target_device_id}/brew-invite/${invite.id}/respond`,
        {
          method: 'POST',
          body: { accept, ...(confirmReplace && { confirm_replace: true }) },
        }
      )
      notify(result.message || inviteStates[result.state] || 'Checking invitation status.')
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setReplacement(null)
      setPending('')
      ;(deviceID ? machine : fleet).reload()
    }
  }
  return (
    <>
      <ErrorBanner message={error} />
      {invites.map((invite) => {
        const device = devices.find((d) => d.id === invite.target_device_id)
        const { water_l, temperature_f, intensity, time_seconds } = invite
        const coffee =
          invite.action === 'z_program' &&
          water_l != null &&
          temperature_f != null &&
          intensity != null &&
          time_seconds != null
        const kitSettings =
          invite.action === 'brew'
            ? [
                invite.selected_abv != null && `Strength ${invite.selected_abv}/10`,
                invite.selected_ibu != null && `Bitterness ${invite.selected_ibu}/10`,
              ]
                .filter(Boolean)
                .join(' · ')
            : ''
        const canRespond = invite.state === 'pending' && Date.parse(invite.expires_at) > Date.now()
        return (
          <article className="panel brew-invitation" key={invite.target_device_id + invite.id}>
            <div>
              <strong>
                {invite.program_name ||
                  invite.recipe_name ||
                  (invite.action === 'rinse'
                    ? 'Rinse'
                    : invite.action === 'clean'
                      ? 'Clean'
                      : 'Group brew')}{' '}
                · {invite.group_name}
              </strong>
              <p>
                {invite.source_name} invited{' '}
                {invite.stock_z_name ||
                  device?.name ||
                  (invite.menu_serial ? 'Zymatic ' + invite.menu_serial : 'this brewer')}
                . {invite.message}
              </p>
              {kitSettings && <p>{kitSettings}</p>}
              {coffee && (
                <p>
                  {Number(water_l.toFixed(2))} L · {temperature(temperature_f, unit)} ·{' '}
                  {time_seconds / 60} min · Intensity {intensity}/15
                </p>
              )}
            </div>
            {canRespond ? (
              <div className="form-actions">
                <Button
                  variant="secondary"
                  disabled={!!pending}
                  onClick={() => void respond(invite, false)}
                >
                  Decline
                </Button>
                <Button
                  disabled={
                    !!pending || (!invite.menu_serial && !invite.stock_z_url && !device?.online)
                  }
                  onClick={() => {
                    if (invite.stock_z_url) {
                      setConfirmIdle(false)
                      setReplacement(invite)
                    } else void respond(invite, true)
                  }}
                >
                  {invite.stock_z_url
                    ? 'Review replacement'
                    : invite.menu_serial
                      ? 'Add to brewer menu'
                      : coffee
                        ? 'Review on brewer'
                        : 'Accept invitation'}
                </Button>
              </div>
            ) : (
              <span>{inviteStates[invite.state] || 'Status unavailable'}</span>
            )}
          </article>
        )
      })}
      {replacement && (
        <Modal
          title={'Replace working recipe on ' + replacement.stock_z_name + '?'}
          onClose={() => {
            if (!pending) setReplacement(null)
          }}
          actions={
            <>
              <Button variant="secondary" disabled={!!pending} onClick={() => setReplacement(null)}>
                Cancel
              </Button>
              <Button
                disabled={!!pending || !confirmIdle}
                onClick={() => void respond(replacement, true, true)}
              >
                Replace working recipe
              </Button>
            </>
          }
        >
          <p>
            {replacement.recipe_name} will become the working program on this Z. Review and start it
            at the dial.
          </p>
          <StockZTemperatureLimits limits={replacement.stock_z_review?.temperature_limits} />
          {replacement.stock_z_review && (
            <TransferLoadingSheet
              source={recipes?.find(
                (r) => r.id === replacement.recipe_id && r.revision === replacement.recipe_revision
              )}
              batchLiters={replacement.stock_z_review.batch_liters}
              ingredients={replacement.stock_z_review.ingredients}
              slots={replacement.stock_z_review.hop_slots_by_minute}
            />
          )}
          <label className="checkbox-row">
            <input
              type="checkbox"
              checked={confirmIdle}
              disabled={!!pending}
              onChange={(e) => setConfirmIdle(e.target.checked)}
            />
            I verified that {replacement.stock_z_name} at {replacement.stock_z_url} is idle.
          </label>
        </Modal>
      )}
    </>
  )
}
