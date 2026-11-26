import { RefreshCw } from 'lucide-react'
import { useHub, useResource } from '../context'
import { modelLabel } from '../lib/utils'
import { Button, ErrorBanner, SectionHeading } from './ui'

export interface NetworkCandidate {
  name: string
  model?: string
  base_url?: string
  source: string
  firmware_version?: string
  serial?: string
}

export function NetworkDiscovery({
  disabled,
  onSelect,
  onClose,
}: {
  disabled: boolean
  onSelect: (item: NetworkCandidate) => void
  onClose: () => void
}) {
  const hub = useHub()
  const result = useResource<{ candidates: NetworkCandidate[]; partial: boolean }>(
    '/devices/discover'
  )
  const candidates = result.data?.candidates || []
  return (
    <section className="network-discovery">
      <SectionHeading
        title="On your network"
        actions={
          <Button variant="ghost" disabled={disabled || result.loading} onClick={result.reload}>
            <RefreshCw size={15} className={result.loading ? 'spin' : ''} />
            Check again
          </Button>
        }
      />
      <ErrorBanner message={result.error} />
      {result.loading ? (
        <p role="status">Looking for PicoBrew devices…</p>
      ) : candidates.length ? (
        <ul className="discovered-machines">
          {candidates.map((item) => {
            const serial = item.serial
            return (
              <li key={item.base_url || item.serial}>
                <div>
                  <strong>{item.name || modelLabel(item.model)}</strong>
                  <small>
                    {[
                      item.base_url || serial,
                      item.source === 'stock' || item.source === 'pico-register'
                        ? 'Original firmware'
                        : '',
                      item.firmware_version,
                    ]
                      .filter(Boolean)
                      .join(' · ')}
                  </small>
                </div>
                {item.source === 'zymatic-sync' && serial ? (
                  <Button
                    variant="secondary"
                    disabled={disabled}
                    onClick={() => {
                      localStorage.setItem('stock-zymatic-serial', serial)
                      onClose()
                      hub.navigate('/recipes')
                    }}
                  >
                    Use for recipe sync
                  </Button>
                ) : item.source === 'pico-register' ||
                  (item.source === 'stock' && item.model === 'z-series') ? (
                  <Button variant="secondary" disabled={disabled} onClick={() => onSelect(item)}>
                    Add stock brewer
                  </Button>
                ) : item.source === 'standalone' && item.base_url ? (
                  <Button variant="secondary" disabled={disabled} onClick={() => onSelect(item)}>
                    Select
                  </Button>
                ) : null}
                {item.source === 'stock' && item.base_url && /^http:\/\//.test(item.base_url) && (
                  <a
                    href={item.model === 'picostill' ? item.base_url + '/data-page' : item.base_url}
                    target="_blank"
                    rel="noreferrer"
                  >
                    Open device website
                  </a>
                )}
              </li>
            )
          })}
        </ul>
      ) : (
        !result.error && (
          <p role="status">No new machines found. You can also enter a machine’s address below.</p>
        )
      )}
      {result.data?.partial && (
        <p className="help-text">
          Only part of the local network was checked. Check again or enter the address below.
        </p>
      )}
    </section>
  )
}
