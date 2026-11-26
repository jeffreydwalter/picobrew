import { test, expect } from '@playwright/test'
import type { Device, Telemetry } from '../src/types'

for (const viewport of [
  { name: 'desktop', width: 1280, height: 900 },
  { name: 'mobile', width: 390, height: 844 },
]) {
  test(`automatic machine identity and simple receiver settings work on ${viewport.name}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport)
    await page.emulateMedia({ colorScheme: viewport.name === 'desktop' ? 'dark' : 'light' })
    const errors: string[] = []
    page.on('pageerror', (error) => errors.push(error.message))
    let device: Device | null = null
    let receiver: Telemetry & { state: string } = {
      enabled: true,
      host: 'hub.local',
      port: 8080,
      state: 'configured',
    }
    let offline = false
    let settingsRequests = 0
    const overrides: unknown[] = []
    let registrations = 0
    await page.route('**/api/v1/auth/session', async (route) => {
      const response = await route.fetch()
      await route.fulfill({ json: { ...(await response.json()), demo: false } })
    })
    // Isolate these physical-machine response fixtures from the real simulator's fleet events.
    await page.route('**/api/v1/events', (route) =>
      route.fulfill({
        contentType: 'text/event-stream',
        body: ': isolated machine fixture\n\n',
      })
    )
    await page.route('**/api/v1/devices**', async (route) => {
      const request = route.request()
      const path = new URL(request.url()).pathname
      let result: unknown = null
      if (path === '/api/v1/devices') {
        if (request.method() === 'POST') {
          const body = request.postDataJSON()
          expect(body.adapter).toBe('pico-standalone')
          expect(body.model).toBe('')
          registrations++
          device = {
            ...body,
            id: 'browser-pico-s',
            model: 'pico-s',
            model_source: 'firmware',
            key_configured: true,
            online: true,
            created_at: '',
            last_seen: null,
            status: null,
            capabilities: {
              api_version: 1,
              firmware_version: '1.1.0-test',
              commands: [],
              recipe_capacity: 32,
              step_capacity: 15,
              front_panel: true,
              history_persistent: true,
              telemetry_forwarding: true,
              dedicated_pause_resume: false,
            },
            telemetry: { ...receiver, state: 'configured' },
          }
          result = device
        } else
          result = device
            ? [
                {
                  ...device,
                  ...(offline ? { online: false, capabilities: null } : {}),
                  telemetry: receiver,
                },
              ]
            : []
      } else if (path.endsWith('/telemetry')) {
        if (request.method() === 'PUT') {
          const body = request.postDataJSON()
          overrides.push(body)
          expect(Object.keys(body).sort()).toEqual(['enabled', 'host', 'port'])
          receiver = { ...body, state: offline ? 'pending' : 'configured' }
          if (device) device.telemetry = receiver
        }
        result = receiver
      } else if (path.endsWith('/settings')) {
        settingsRequests++
        result = { temperature_unit: 'F', language: 'en', detailed_status: true }
      } else if (path.endsWith('/recipes')) result = []
      else if (path.endsWith('/ai-monitor'))
        result = { enabled: false, state: 'off', configured: false }
      else if (path.endsWith('/test') || path.endsWith('/browser-pico-s')) result = device
      await route.fulfill({ json: result })
    })

    await page.goto('/#/overview')
    await page.getByRole('button', { name: 'Add machine', exact: true }).click()
    let dialog = page.getByRole('dialog', { name: 'Add machine', exact: true })
    await expect(dialog.getByLabel('Machine model')).toHaveValue('Detect automatically')
    await expect(dialog.getByRole('combobox', { name: 'Machine model' })).toHaveCount(0)
    await dialog.getByLabel('Machine name').fill('Automatically connected Pico S')
    await page.route('**/api/v1/usb/devices', (route) =>
      route.fulfill({
        json: {
          available: true,
          devices: [
            { port: 'test-adapter', label: 'Brewer USB', models: ['pico-s', 'pico-pro', 'pico-c'] },
          ],
        },
      })
    )
    await dialog.getByRole('button', { name: 'USB setup and recovery' }).click()
    const setup = page.getByRole('dialog', { name: 'USB setup and recovery' })
    await setup.getByLabel('Machine model').selectOption('pico-s')
    await expect(setup.getByRole('button', { name: 'Install standalone firmware' })).toBeEnabled()
    await setup.getByLabel('Machine model').selectOption('pico-c')
    await expect(setup.getByRole('button', { name: 'Install standalone firmware' })).toBeDisabled()
    await setup.getByLabel('Physical Pico C display controller').selectOption('ssd1309')
    await setup.getByLabel('USB operation').selectOption('backup')
    await expect(setup.getByRole('button', { name: 'Back up original firmware' })).toBeEnabled()
    await expect(setup.locator('input[type=file], input[type=password]')).toHaveCount(0)
    expect(await setup.evaluate((d) => d.scrollWidth <= d.clientWidth + 1)).toBe(true)
    await setup.getByRole('button', { name: 'Back to Add machine' }).click()
    await expect(dialog.getByLabel('Machine name')).toHaveValue('Automatically connected Pico S')
    expect(registrations).toBe(0)
    await dialog.getByLabel('Machine URL').fill('http://192.168.1.42')
    await dialog.getByRole('button', { name: 'Use an existing key instead' }).click()
    await dialog.getByLabel('Machine API key').fill('isolated-browser-key')
    await dialog.getByRole('button', { name: 'Add machine', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    const card = page
      .locator('.machine-card')
      .filter({ has: page.getByRole('heading', { name: 'Automatically connected Pico S' }) })
    await expect(card.getByText('Pico S', { exact: true })).toBeVisible()
    expect(overrides).toHaveLength(0)
    expect(registrations).toBe(1)
    await card.getByRole('link', { name: 'Open Automatically connected Pico S controls' }).click()
    await page.getByRole('button', { name: 'Manage machine' }).click()
    const management = page.getByRole('dialog', { name: 'Machine management' })
    const keySection = management.getByRole('region', { name: 'Device API key' })
    await expect(keySection.getByRole('button', { name: 'Copy key' })).toBeEnabled()
    expect(await management.evaluate((d) => d.scrollWidth <= d.clientWidth + 1)).toBe(true)
    await management.screenshot({ path: test.info().outputPath('machine-management.png') })
    await page.getByRole('button', { name: 'Brewer preferences' }).click()
    dialog = page.getByRole('dialog', { name: 'Brewer preferences' })
    await expect(dialog.getByLabel('Receiver host')).toHaveValue('hub.local')
    await expect(dialog.getByLabel('Receiver port')).toHaveValue('8080')
    const liveReadings = dialog.getByRole('region', { name: 'Live readings' })
    await expect(liveReadings.getByRole('combobox')).toHaveCount(0)
    await expect(dialog.getByText('Advanced Hub connection', { exact: true })).toHaveCount(0)
    await expect(dialog.getByText('Return to automatic', { exact: true })).toHaveCount(0)
    await dialog.getByLabel('Receiver host').fill('brew-relay.local')
    await dialog.getByLabel('Receiver port').fill('49152')
    await dialog.getByLabel('Send live readings').uncheck()
    await dialog.getByRole('button', { name: 'Save connection' }).click()
    await expect(liveReadings.getByRole('status')).toHaveText('Saved. Live readings are off.')
    expect(overrides).toEqual([{ enabled: false, host: 'brew-relay.local', port: 49152 }])
    await page.screenshot({
      path: test.info().outputPath(`machine-connection-${viewport.name}.png`),
      fullPage: true,
    })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
      true
    )
    await dialog.getByRole('button', { name: 'Close dialog' }).click()
    await page.getByRole('button', { name: 'Manage machine' }).click()
    await page.getByRole('button', { name: 'Test connection' }).click()
    await page.getByRole('button', { name: 'Manage machine' }).click()
    await page.getByRole('button', { name: 'Brewer preferences' }).click()
    dialog = page.getByRole('dialog', { name: 'Brewer preferences' })
    await expect(dialog.getByLabel('Receiver host')).toHaveValue('brew-relay.local')
    await expect(dialog.getByLabel('Receiver port')).toHaveValue('49152')
    await expect(dialog.getByLabel('Send live readings')).not.toBeChecked()
    expect(overrides).toHaveLength(1)

    // A disconnected machine keeps its saved connection editable even without capabilities.
    await dialog.getByRole('button', { name: 'Close dialog' }).click()
    offline = true
    receiver = { ...receiver, state: 'pending' }
    const previousSettingsRequests = settingsRequests
    await page.reload()
    await page.getByRole('button', { name: 'Manage machine' }).click()
    await expect(page.getByRole('button', { name: 'Brewer preferences' })).toBeEnabled()
    await page.getByRole('button', { name: 'Brewer preferences' }).click()
    dialog = page.getByRole('dialog', { name: 'Brewer preferences' })
    await expect(dialog.getByLabel('Receiver host')).toHaveValue('brew-relay.local')
    await expect(dialog.getByLabel('Send live readings')).not.toBeChecked()
    await expect(
      dialog.getByText('Connect the machine to change its display preferences.')
    ).toBeVisible()
    expect(settingsRequests).toBe(previousSettingsRequests)
    await dialog.getByLabel('Receiver host').fill('offline-saved.local')
    await dialog.getByRole('button', { name: 'Save connection' }).click()
    await expect(
      dialog.getByRole('region', { name: 'Live readings' }).getByRole('status')
    ).toHaveText('Saved. Will apply when the machine is available.')
    expect(overrides[1]).toEqual({ enabled: false, host: 'offline-saved.local', port: 49152 })
    await page.screenshot({
      path: test.info().outputPath(`machine-connection-offline-${viewport.name}.png`),
      fullPage: true,
    })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
      true
    )
    await dialog.getByRole('button', { name: 'Close dialog' }).click()
    await page.reload()
    await page.getByRole('button', { name: 'Manage machine' }).click()
    await page.getByRole('button', { name: 'Brewer preferences' }).click()
    dialog = page.getByRole('dialog', { name: 'Brewer preferences' })
    await expect(dialog.getByLabel('Receiver host')).toHaveValue('offline-saved.local')
    await expect(dialog.getByLabel('Send live readings')).not.toBeChecked()
    expect(errors).toEqual([])
    expect(registrations).toBe(1)
  })
}
