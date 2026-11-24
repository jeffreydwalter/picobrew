import { lazy, Suspense, useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import {
  LayoutDashboard,
  BookOpen,
  History,
  Settings2,
  Code2,
  Wheat,
  Menu,
  X,
  LogOut,
  Check,
  TriangleAlert,
  ArrowRight,
  LockKeyhole,
  Radio,
  ChevronDown,
} from 'lucide-react'
import { deviceState } from './lib/utils'
import { api, errorMessage, setCSRF } from './lib/api'
import type { Session, Device, Recipe, Brew, Profile, Unit } from './types'
import { HubContext } from './context'
import { Button, Field, ErrorBanner, Loading, IconButton } from './components/ui'
import { RecipeWorkspaceTabs, recipeViewForRoute } from './components/RecipeWorkspaceTabs'
const Fleet = lazy(() => import('./pages/Fleet'))
const Machine = lazy(() => import('./pages/Machine'))
const Recipes = lazy(() => import('./pages/Recipes'))
const Catalog = lazy(() => import('./pages/Catalog'))
const HistoryPage = lazy(() => import('./pages/History'))
const Settings = lazy(() => import('./pages/Settings'))
const Assistant = lazy(() => import('./pages/Assistant'))
const Explorer = lazy(() => import('./pages/Explorer'))
const navigation = [
  { path: '/overview', label: 'Brewery', icon: LayoutDashboard },
  { path: '/recipes', label: 'Recipes', icon: BookOpen },
  { path: '/history', label: 'Brew journal', icon: History },
  { path: '/settings', label: 'Settings', icon: Settings2 },
  { path: '/api', label: 'API explorer', icon: Code2 },
]
function MachinePicker({ devices, current }: { devices: Device[]; current: string }) {
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  useEffect(() => {
    if (!open) return
    function dismiss(event: PointerEvent) {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', dismiss)
    return () => document.removeEventListener('pointerdown', dismiss)
  }, [open])
  return (
    <div
      className="machine-picker"
      ref={root}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false)
      }}
      onKeyDown={(event) => {
        if (open && event.key === 'Escape') {
          event.preventDefault()
          setOpen(false)
          trigger.current?.focus()
        }
      }}
    >
      <button
        ref={trigger}
        aria-label="Switch machine"
        aria-expanded={open}
        aria-controls="breadcrumb-machines"
        onClick={() => setOpen(!open)}
      >
        Machine <ChevronDown size={14} />
      </button>
      <ul id="breadcrumb-machines" aria-label="Machines" hidden={!open}>
        {devices.map((device) => (
          <li key={device.id}>
            <a
              href={`#/devices/${device.id}`}
              aria-current={device.id === current ? 'page' : undefined}
              onClick={() => {
                setOpen(false)
                trigger.current?.focus()
              }}
            >
              <span>{device.name}</span>
              <small>{deviceState(device)}</small>
              {device.id === current && <Check size={15} aria-label="Current machine" />}
            </a>
          </li>
        ))}
      </ul>
    </div>
  )
}
export default function App() {
  const [session, setSession] = useState<Session | null>(null)
  const [authError, setAuthError] = useState('')
  const [route, setRoute] = useState(location.hash.slice(1) || '/overview')
  const [mobile, setMobile] = useState(false)
  const [devices, setDevices] = useState<Device[]>([])
  const [recipes, setRecipes] = useState<Recipe[]>([])
  const [brews, setBrews] = useState<Brew[]>([])
  const [profiles, setProfiles] = useState<Profile[]>([])
  const [loading, setLoading] = useState(true)
  const [connected, setConnected] = useState(false)
  const [draft, setDraft] = useState<Recipe | null>(null)
  const [unit, setUnitState] = useState<Unit>(() =>
    localStorage.getItem('hub-temperature-unit') === 'C' ? 'C' : 'F'
  )
  const [toasts, setToasts] = useState<{ id: number; message: string; kind: string }[]>([])
  const notify = useCallback((message: string, kind: 'success' | 'error' = 'success') => {
    const id = Date.now() + Math.random()
    setToasts((items) => [...items, { id, message, kind }])
    setTimeout(() => setToasts((items) => items.filter((item) => item.id !== id)), 6000)
  }, [])
  const loadSession = useCallback(async () => {
    try {
      const value = await api<Session>('/auth/session')
      setSession(value)
      setCSRF(value.csrf_token)
      setAuthError('')
    } catch (error) {
      setAuthError(errorMessage(error))
    }
  }, [])
  const fleetRequest = useRef(0)
  const refreshFleet = useCallback(async (signal?: AbortSignal) => {
    const request = ++fleetRequest.current
    try {
      const fleet = await api<Device[]>('/devices', { signal })
      if (!signal?.aborted && request === fleetRequest.current) {
        setDevices(fleet || [])
        setConnected(true)
      }
    } catch (error) {
      if (!signal?.aborted && request === fleetRequest.current) setConnected(false)
      throw error
    }
  }, [])
  const refresh = useCallback(
    async (signal?: AbortSignal) => {
      const results = await Promise.allSettled([
        refreshFleet(signal),
        api<Recipe[]>('/recipes', { signal }).then((value) => {
          if (!signal?.aborted) setRecipes(value || [])
        }),
        api<Brew[]>('/brews', { signal }).then((value) => {
          if (!signal?.aborted) setBrews(value || [])
        }),
        api<Profile[]>('/profiles', { signal }).then((value) => {
          if (!signal?.aborted) setProfiles(value || [])
        }),
      ])
      if (signal?.aborted) return
      for (const result of results)
        if (result.status === 'rejected') notify(errorMessage(result.reason), 'error')
      setLoading(false)
    },
    [notify, refreshFleet]
  )
  useEffect(() => {
    void loadSession()
    const listener = () => {
      void loadSession()
    }
    window.addEventListener('hub:unauthorized', listener)
    return () => window.removeEventListener('hub:unauthorized', listener)
  }, [loadSession])
  useEffect(() => {
    const listener = () => {
      setRoute(location.hash.slice(1) || '/overview')
      setMobile(false)
      window.scrollTo(0, 0)
    }
    window.addEventListener('hashchange', listener)
    return () => window.removeEventListener('hashchange', listener)
  }, [])
  useEffect(() => {
    if (route === '/settings/new-device') {
      history.replaceState(null, '', '#/overview/new-device')
      setRoute('/overview/new-device')
    }
    if (route.split('?')[0] === '/assistant') {
      const query = new URLSearchParams(route.split('?')[1] || '')
      const brew = query.get('brew')
      const recipe = query.get('recipe')
      const destination = brew
        ? `/history/${encodeURIComponent(brew)}?analysis=1`
        : `/recipes/create-with-ai${recipe ? '?recipe=' + encodeURIComponent(recipe) : ''}`
      history.replaceState(null, '', '#' + destination)
      setRoute(destination)
    }
  }, [route])
  useEffect(() => {
    if (!session?.authenticated) return
    // A permanent event stream per tab exhausts the HTTP/1 connection pool on LAN hubs.
    const controller = new AbortController()
    let pending = false
    let nextFullRefresh = 0
    async function poll() {
      if (pending || controller.signal.aborted) return
      pending = true
      try {
        if (Date.now() >= nextFullRefresh) {
          nextFullRefresh = Date.now() + 45000
          await refresh(controller.signal)
        } else {
          await refreshFleet(controller.signal)
        }
      } catch {
        // The connection indicator reflects fleet errors; the next poll retries.
      } finally {
        pending = false
      }
    }
    function pollVisible() {
      if (!document.hidden) void poll()
    }
    void poll()
    const interval = setInterval(pollVisible, 3000)
    document.addEventListener('visibilitychange', pollVisible)
    return () => {
      controller.abort()
      clearInterval(interval)
      document.removeEventListener('visibilitychange', pollVisible)
    }
  }, [session?.authenticated, refresh, refreshFleet])
  function navigate(path: string) {
    location.hash = path
    if (path === route) setMobile(false)
  }
  function setUnit(value: Unit) {
    setUnitState(value)
    localStorage.setItem('hub-temperature-unit', value)
  }
  if (!session)
    return (
      <div className="boot-screen">
        <Wheat size={38} />
        {authError ? (
          <ErrorBanner message={authError} retry={() => void loadSession()} />
        ) : (
          <Loading />
        )}
      </div>
    )
  if (!session.authenticated) return <AuthPage session={session} onComplete={loadSession} />
  const parts = route.split('?')[0].split('/').filter(Boolean)
  const section = parts[0] || 'overview'
  const recipeView = recipeViewForRoute(route)
  const navigationSection = recipeView ? 'recipes' : section === 'devices' ? 'overview' : section
  const selected = navigation.find((item) => item.path === `/${navigationSection}`) || navigation[0]
  return (
    <HubContext.Provider
      value={{
        session,
        devices,
        recipes,
        brews,
        profiles,
        loading,
        connected,
        unit,
        setUnit,
        refresh,
        notify,
        navigate,
        draft,
        setDraft,
      }}
    >
      <div
        className="app-shell"
        onKeyDown={(event) => {
          if (
            mobile &&
            event.key === 'Escape' &&
            !event.defaultPrevented &&
            !document.querySelector('dialog[open]')
          ) {
            event.preventDefault()
            setMobile(false)
            document.querySelector<HTMLButtonElement>('.mobile-nav-button')?.focus()
          }
        }}
      >
        {mobile && <div className="sidebar-scrim" onClick={() => setMobile(false)} />}
        <aside className={`sidebar ${mobile ? 'open' : ''}`}>
          <a href="#/overview" className="brand">
            <span className="brand-mark">
              <Wheat size={24} />
            </span>
            <div>
              PicoBrew <strong>Hub</strong>
              <small>YOUR BREWERY, TOGETHER</small>
            </div>
          </a>
          <nav aria-label="Main navigation">
            {navigation.map((item) => (
              <a
                key={item.path}
                href={`#${item.path}`}
                className={navigationSection === item.path.slice(1) ? 'active' : ''}
                aria-current={navigationSection === item.path.slice(1) ? 'page' : undefined}
              >
                <item.icon size={19} />
                <span>{item.label}</span>
                {item.path === '/recipes' && <small>{recipes.length}</small>}
              </a>
            ))}
          </nav>
          <div className="sidebar-bottom">
            <div className="local-status">
              <i className="online-dot online" />
              Independent by design
            </div>
            <p>
              Your machines. Your recipes.
              <br />
              Your brew history.
            </p>
            <small>{session.version ? `Hub ${session.version}` : 'Hub'}</small>
          </div>
        </aside>
        <div className="main-shell">
          <header className="topbar">
            <div>
              <IconButton
                label="Open navigation"
                className="mobile-nav-button"
                onClick={() => setMobile(true)}
              >
                <Menu size={21} />
              </IconButton>
              <nav className="breadcrumb" aria-label="Breadcrumb">
                {section === 'overview' ? (
                  <strong aria-current="page">Brewery</strong>
                ) : (
                  <>
                    {' '}
                    <a className="breadcrumb-parent" href="#/overview">
                      Brewery
                    </a>
                    <span className="breadcrumb-separator" aria-hidden="true">
                      /
                    </span>
                    {section === 'devices' && devices.length > 1 ? (
                      <MachinePicker key={parts[1]} devices={devices} current={parts[1]} />
                    ) : (
                      <strong aria-current="page">
                        {section === 'devices' ? 'Machine' : selected.label}
                      </strong>
                    )}
                  </>
                )}
              </nav>
            </div>
            <div className="topbar-right">
              {session.demo && <span className="simulation-badge">SIMULATION</span>}
              <span className={`connection-label ${connected ? 'connected' : ''}`}>
                <i />
                {connected ? 'Live connection' : 'Reconnecting…'}
              </span>
              <button className="unit-toggle" onClick={() => setUnit(unit === 'F' ? 'C' : 'F')}>
                °{unit}
              </button>
              <IconButton
                label="Sign out"
                onClick={async () => {
                  try {
                    await api('/auth/logout', { method: 'POST' })
                    await loadSession()
                  } catch (error) {
                    notify(errorMessage(error), 'error')
                  }
                }}
              >
                <LogOut size={17} />
              </IconButton>
            </div>
          </header>
          <main className="main-content">
            {session.demo && (
              <div className="demo-notice">
                <Radio size={15} />
                <span>Simulation · Controls operate virtual brewers.</span>
              </div>
            )}
            {recipeView && <RecipeWorkspaceTabs route={route} />}
            <Suspense fallback={<Loading />}>
              {section === 'devices' ? (
                <Machine
                  key={route}
                  id={parts[1]}
                  initialRecipe={
                    new URLSearchParams(route.split('?')[1]).get('recipe') || undefined
                  }
                />
              ) : section === 'recipes' ? (
                parts[1] === 'create-with-ai' ? (
                  <Assistant key={route} />
                ) : (
                  <Recipes key={parts[1] || 'list'} id={parts[1]} />
                )
              ) : section === 'catalog' ? (
                <Catalog key={parts[1] || 'list'} id={parts[1]} />
              ) : section === 'history' ? (
                <HistoryPage
                  key={parts[1] || 'list'}
                  id={parts[1]}
                  analyze={new URLSearchParams(route.split('?')[1] || '').get('analysis') === '1'}
                />
              ) : section === 'settings' ? (
                <Settings />
              ) : section === 'assistant' ? (
                <Assistant />
              ) : section === 'api' ? (
                <Explorer />
              ) : (
                <Fleet
                  addMachine={parts[1] === 'new-device'}
                  usbJobId={
                    new URLSearchParams(route.split('?')[1] || '').get('usb_job_id') || undefined
                  }
                />
              )}
            </Suspense>
          </main>
          <footer className="app-footer">
            <span>
              PicoBrew Hub <span>·</span> Crafted for your brew day
            </span>
            <span>
              {devices.filter((device) => device.online).length} of {devices.length} machines
              connected
            </span>
          </footer>
        </div>
        <div className="toasts" aria-live="polite">
          {toasts.map((toast) => (
            <div
              key={toast.id}
              className={`toast ${toast.kind}`}
              role={toast.kind === 'error' ? 'alert' : 'status'}
            >
              {toast.kind === 'error' ? <TriangleAlert size={18} /> : <Check size={18} />}
              <span>{toast.message}</span>
              <button
                aria-label="Dismiss notification"
                onClick={() => setToasts((items) => items.filter((item) => item.id !== toast.id))}
              >
                <X size={15} />
              </button>
            </div>
          ))}
        </div>
      </div>
    </HubContext.Provider>
  )
}
function AuthPage({ session, onComplete }: { session: Session; onComplete: () => Promise<void> }) {
  const [password, setPassword] = useState('')
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api(session.setup_required ? '/auth/setup' : '/auth/login', {
        method: 'POST',
        body: session.setup_required ? { token, password } : { password },
      })
      await onComplete()
    } catch (error) {
      setError(errorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="auth-page">
      <div className="auth-story">
        <div className="brand inverse">
          <span className="brand-mark">
            <Wheat size={27} />
          </span>
          <div>
            PicoBrew <strong>Hub</strong>
          </div>
        </div>
        <div>
          <p className="eyebrow">FROM RECIPE TO LAST POUR</p>
          <h1>
            A little more craft.
            <br />A lot more control.
          </h1>
          <p>Bring your brewers, recipes and brew days together in one place.</p>
          <div className="auth-lines">
            <span />
            <span />
            <span />
          </div>
        </div>
        <small>Local first. Made for the machines you love.</small>
      </div>
      <div className="auth-form">
        <LockKeyhole size={28} />
        <p className="eyebrow">YOUR BREWERY</p>
        <h2>{session.setup_required ? 'Make yourself at home.' : 'Welcome back.'}</h2>
        <p>
          {session.setup_required
            ? 'Use the setup token printed by your hub and choose a Hub password.'
            : 'Sign in to monitor your machines and plan the next brew.'}
        </p>
        <form onSubmit={submit}>
          {session.setup_required && (
            <Field label="Setup token">
              <input
                value={token}
                onChange={(event) => setToken(event.target.value)}
                autoComplete="off"
                required
              />
            </Field>
          )}
          <Field label={session.setup_required ? 'Choose a password' : 'Password'}>
            <input
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete={session.setup_required ? 'new-password' : 'current-password'}
              required
              minLength={session.setup_required ? 12 : 1}
            />
          </Field>
          <ErrorBanner message={error} />
          <Button type="submit" disabled={busy}>
            {busy ? 'Connecting…' : session.setup_required ? 'Set up Hub' : 'Sign in'}
            <ArrowRight size={17} />
          </Button>
        </form>
      </div>
    </div>
  )
}
