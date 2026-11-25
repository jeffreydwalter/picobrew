import { expect, test } from '@playwright/test'
import type { BrewDetail, Capabilities, Device, DeviceStatus } from '../src/types'

for (const width of [1280, 390]) {
  for (const model of ['picoferm', 'picostill', 'z-series']) {
    test(`${model} capability controls and history at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      const errors: string[] = [],
        reads: string[] = [],
        commands: Record<string, unknown>[] = []
      page.on('pageerror', (error) => errors.push(error.message))
      const brewing = model === 'z-series',
        ferm = model === 'picoferm'
      const name = ferm ? 'Cellar PicoFerm' : brewing ? 'Bench Pico Z' : 'Studio PicoStill'
      const caps: Capabilities = {
        model,
        model_family: model,
        api_version: 1,
        history_sample_interval_ms: ferm ? 720000 : 15000,
        firmware_version: '1.2.0-fixture',
        commands: brewing
          ? [
              'encoder',
              'stop',
              'clear-stop',
              'manual',
              'clean',
              'drain',
              'chill',
              'pause',
              'resume',
              'resume-interrupted',
              'discard-interrupted',
            ]
          : ferm
            ? ['start', 'stop', 'calibrate', 'sample', 'set-sampling-interval']
            : ['start', 'essential-oils', 'clean', 'stop', 'set-pump', 'set-fan'],
        command_fields: {
          calibrate: { allowed: ['confirm'], required: ['confirm'] },
          'resume-interrupted': { allowed: ['confirm'], required: ['confirm'] },
          'discard-interrupted': { allowed: [], required: [] },
          'set-pump': { allowed: ['enabled'], required: ['enabled'] },
          'set-fan': { allowed: ['channel', 'level', 'enabled'], required: ['channel', 'level'] },
          'set-sampling-interval': { allowed: ['value'], required: ['value'] },
        },
        settings_fields: brewing
          ? ['temperature_unit', 'language', 'detailed_status']
          : ['temperature_unit'],
        z_program_maker: brewing,
        recipe_capacity: brewing ? 16 : 0,
        step_capacity: brewing ? 15 : 0,
        front_panel: brewing,
        history_persistent: true,
        telemetry_forwarding: true,
        dedicated_pause_resume: false,
        capabilities: {
          recipes: brewing,
          brewing,
          heating: brewing,
          front_panel: brewing,
          temperature: true,
          pressure: !brewing,
          battery: ferm,
          calibration: ferm,
          fermentation: ferm,
          pump: !ferm,
          fans: !ferm && !brewing,
        },
      }
      const status: DeviceStatus = {
        model,
        model_family: model,
        uptime_ms: 60000,
        device_uid: 'family-browser-fixture',
        ready: true,
        busy: false,
        stopped: false,
        state: 'idle',
        recovery: brewing
          ? {
              available: true,
              valid: true,
              recipe_name: 'Saved pale ale',
              step_name: '',
              current_step: -1,
              step_seconds: 0,
              elapsed_seconds: 125,
            }
          : null,
        command: 'idle',
        device_state: 0,
        phase: 4,
        controller_status: 0,
        compartment: 0,
        wort_f: brewing ? 72 : null,
        thermoblock_f: brewing ? 73 : null,
        target_f: brewing ? 0 : null,
        step_seconds: 0,
        current_step: -1,
        step_count: 0,
        recipe_name: '',
        step_name: '',
        session_id: '',
        filter_inserted: null,
        stop_reason: 'none',
        encoder: { position: 0, min: 0, max: 5 },
        temperature_f: 68.5,
        pressure_kpa: ferm ? null : 6.89,
        battery_v: ferm ? 3.8 : null,
        sensors: brewing
          ? []
          : ferm
            ? [
                {
                  id: 'temperature',
                  label: 'Temperature',
                  kind: 'temperature',
                  unit: 'F',
                  value: 68.5,
                },
                {
                  id: 'pressure_native',
                  label: 'Native pressure',
                  kind: 'pressure',
                  unit: 'raw',
                  value: 532.125,
                },
                { id: 'battery', label: 'Battery', kind: 'voltage', unit: 'V', value: 3.8 },
              ]
            : [
                { id: 'inlet', label: 'Inlet', kind: 'temperature', unit: 'F', value: 152 },
                { id: 'pot', label: 'Pot', kind: 'temperature', unit: 'F', value: 170 },
                { id: 'pressure', label: 'Pressure', kind: 'pressure', unit: 'kPa', value: 6.89 },
              ],
        actuators:
          !ferm && !brewing
            ? [
                { id: 'pump', label: 'Pump', kind: 'pump', enabled: false, level: null, rpm: null },
                { id: 'fan1', label: 'Fan 1', kind: 'fan', enabled: true, level: 25, rpm: 1200 },
                { id: 'fan2', label: 'Fan 2', kind: 'fan', enabled: false, level: 0, rpm: 0 },
              ]
            : [],
      }
      const device: Device = {
        id: 'family-device',
        name,
        model,
        model_source: 'firmware',
        adapter: 'pico-standalone',
        enabled: true,
        online: true,
        base_url: 'http://192.168.1.77',
        location: 'Cellar',
        key_configured: true,
        created_at: '',
        last_seen: null,
        status,
        capabilities: caps,
      }
      const history: BrewDetail = {
        id: 'family-device-1',
        device_id: device.id,
        device_name: name,
        model,
        recipe_name: '',
        state: 'completed',
        command: 'start',
        started_at: null,
        observed_at: '2026-11-20T00:00:00Z',
        duration_ms: 15000,
        device_history_id: 1,
        gap_count: 0,
        truncated: false,
        notes: '',
        rating: 0,
        sample_count: 2,
        source: 'device',
        sample_fields: [
          'elapsed_ms',
          'wort_f',
          'thermoblock_f',
          'target_f',
          'step_seconds',
          'phase',
          'controller_status',
          'compartment',
          'current_step',
          'flags',
          'device_state',
          'temperature_f',
          'pressure_kpa',
          'battery_v',
          'sensor_pressure_native',
        ],
        field_metadata: {
          sensor_pressure_native: { label: 'Native pressure', unit: 'raw', kind: 'pressure' },
        },
        samples: [0, 15000].map((elapsed, index) => ({
          elapsed_ms: elapsed,
          wort_f: null,
          thermoblock_f: null,
          target_f: null,
          step_seconds: 0,
          phase: 0,
          controller_status: 0,
          compartment: 0,
          current_step: 255,
          flags: 3,
          device_state: 2,
          source: 'device',
          temperature_f: 68.5 + index,
          pressure_kpa: null,
          battery_v: 3.8,
          readings: { sensor_pressure_native: 532.125 + index },
        })),
      }
      await page.route('**/api/v1/events', (route) =>
        route.fulfill({ contentType: 'text/event-stream', body: ': isolated family fixture\n\n' })
      )
      await page.route('**/api/v1/brews**', (route) =>
        route.fulfill({
          json: new URL(route.request().url()).pathname === '/api/v1/brews' ? [history] : history,
        })
      )
      let installed = {
        id: 'oem-program',
        name: 'Adjunct pause',
        steps: [],
        z_program: {
          Name: 'Adjunct pause',
          IsBeer: true,
          UsePicoPakMode: true,
          Steps: [
            { Name: 'Hops', StepType: 1, Location: 6, Temp: 96.25, Time: 185, Drain: 7 },
            { Name: 'Inspect', StepType: 2, Time: 31 },
          ],
        },
      }
      await page.route('**/api/v1/devices**', async (route) => {
        const request = route.request(),
          path = new URL(request.url()).pathname
        reads.push(request.method() + ' ' + path)
        let result: unknown,
          code = 200
        if (path === '/api/v1/devices') {
          if (device.online && device.status) device.last_seen = new Date().toISOString()
          result = [device]
        } else if (path.endsWith('/brew-invite')) {
          result = { invite: null }
        } else if (path.endsWith('/display')) {
          expect(brewing).toBe(true)
          result = {
            width: 128,
            height: 64,
            rotation: 0,
            format: 'row-gray4-msb-hex',
            data: '0123456789abcdef'.repeat(512),
          }
        } else if (path.endsWith('/recipes')) {
          expect(brewing).toBe(true)
          result = { recipes: [installed] }
        } else if (path.endsWith('/recipes/oem-program') && request.method() === 'PUT') {
          const body = request.postDataJSON()
          expect(body.expected).toEqual(installed)
          expect(body.recipe.z_program.Steps).toEqual([
            { ...installed.z_program.Steps[0], Time: 187 },
            installed.z_program.Steps[1],
          ])
          installed = body.recipe
          result = { verified: true }
        } else if (path.endsWith('/ai-monitor')) {
          expect(brewing).toBe(true)
          result = { configured: false, enabled: false, state: 'off' }
        } else if (path.endsWith('/settings'))
          result = brewing
            ? { temperature_unit: 'F', language: 'en', detailed_status: false }
            : { temperature_unit: 'F' }
        else if (path.endsWith('/telemetry'))
          result = { enabled: false, host: '', port: 8080, state: 'disabled' }
        else if (path.endsWith('/commands')) {
          expect(request.method()).toBe('POST')
          expect(request.headers()['idempotency-key']).toBeTruthy()
          expect(request.headers()['x-csrf-token']).toBeTruthy()
          const body = request.postDataJSON()
          commands.push(body)
          if (['resume-interrupted', 'discard-interrupted'].includes(body.command))
            Object.assign(status.recovery!, { available: false, valid: true })
          if (brewing && body.command === 'stop')
            Object.assign(status, {
              stopped: true,
              busy: false,
              state: 'idle',
              command: 'idle',
              stop_reason: 'requested',
            })
          if (brewing && body.command === 'clear-stop')
            Object.assign(status, { stopped: false, stop_reason: 'none' })
          code = 202
          result = { id: 'receipt', state: 'accepted', command: body }
        } else if (ferm && path.endsWith('/brewer') && request.method() === 'GET') {
          result = { brewer_id: '', revision: 0 }
        } else if (path.endsWith('/sync-history')) result = { synced: true }
        else if (path.endsWith('/test') || path.endsWith('/family-device')) result = device
        else throw new Error('Unexpected device request ' + path)
        await route.fulfill({ status: code, json: result })
      })
      await page.goto('/#/devices/family-device')
      await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
      if (ferm)
        await expect(page.getByRole('combobox', { name: 'Brewer for this PicoFerm' })).toHaveValue(
          ''
        )
      expect(commands).toEqual([])
      if (brewing) {
        const idleLock = page.getByRole('button', { name: 'Lock outputs', exact: true })
        await expect(idleLock).toBeEnabled()
        await expect(idleLock).toHaveAccessibleDescription(
          /Prevent heating and pumping until you choose Unlock outputs/
        )
        await expect(idleLock).toHaveAccessibleDescription(
          /discards the interrupted brew checkpoint/
        )
        await page.getByLabel('Recipe on this machine', { exact: true }).selectOption(installed.id)
        await page.getByRole('button', { name: 'Edit saved program', exact: true }).click()
        const editor = page.getByRole('dialog', { name: 'Edit the machine’s saved program' })
        await expect(editor.getByLabel('Flow path')).toHaveValue('6')
        await expect(
          editor.getByText('The Z waits here until the operator continues.')
        ).toBeVisible()
        await editor.getByLabel('Hold (min:sec)').fill('3:07')
        await editor.getByRole('button', { name: 'Save to machine' }).click()
        await expect(editor).not.toBeVisible()
        const canvas = page.locator('canvas[aria-label="Live Pico OLED display"]')
        await expect(canvas).toBeVisible()
        await expect(page.getByRole('button', { name: 'Pause session' })).toHaveCount(0)
        await expect(page.getByRole('button', { name: 'Resume session' })).toHaveCount(0)
        const resumeCheckpoint = page.getByRole('button', {
          name: 'Resume interrupted brew',
          exact: true,
        })
        for (const condition of [{ ready: false }, { filter_inserted: false }]) {
          Object.assign(status, { ready: true, filter_inserted: true }, condition)
          await page.reload()
          await expect(resumeCheckpoint).toBeDisabled()
        }
        Object.assign(status, { ready: true, filter_inserted: true })
        await page.reload()
        await page.getByRole('button', { name: 'Other programs', exact: true }).click()
        await expect(page.getByLabel('Other programs', { exact: true })).toContainText('Clean')
        await expect(page.getByLabel('Other programs', { exact: true })).toContainText('Chill')
        const pixels = await canvas.evaluate((node) =>
          Array.from((node as HTMLCanvasElement).getContext('2d')!.getImageData(0, 0, 16, 1).data)
        )
        expect(pixels.slice(0, 4)).toEqual([28, 43, 35, 255])
        expect(pixels.slice(-4)).toEqual([196, 219, 171, 255])
        await page.getByRole('button', { name: 'Press dial', exact: true }).click()
        await expect.poll(() => commands.length).toBe(1)
        expect(commands[0]).toEqual({ command: 'encoder', click: true })
        await expect(page.getByRole('button', { name: 'Open program', exact: true })).toBeDisabled()
        await expect(page.getByText('Review checkpoint', { exact: true })).toBeVisible()
        expect(
          await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
        ).toBe(true)
        await resumeCheckpoint.click()
        let dialog = page.getByRole('dialog', { name: 'Resume Saved pale ale?' })
        await expect(dialog).toContainText('water, vessel and filter')
        await expect(dialog).toContainText('30 seconds')
        await expect(dialog).toContainText('saved remaining step time')
        await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
        expect(commands).toHaveLength(1)
        await page.getByRole('button', { name: 'Resume interrupted brew', exact: true }).click()
        await page
          .getByRole('dialog')
          .getByRole('button', { name: 'Resume brew', exact: true })
          .click()
        await expect.poll(() => commands.length).toBe(2)
        expect(commands[1]).toEqual({ command: 'resume-interrupted', confirm: true })
        await expect(
          page.getByRole('button', { name: 'Resume interrupted brew', exact: true })
        ).toHaveCount(0)
        Object.assign(status.recovery!, { available: false, valid: false })
        await page.getByRole('button', { name: 'Manage machine' }).click()
        await page.getByRole('button', { name: 'Test connection', exact: true }).click()
        await expect(
          page.getByText(
            'The saved checkpoint could not be verified. Discard it before starting another recipe.',
            { exact: true }
          )
        ).toBeVisible()
        await expect(page.getByRole('button', { name: 'Open program', exact: true })).toBeDisabled()
        await page.getByRole('button', { name: 'Discard checkpoint', exact: true }).click()
        dialog = page.getByRole('dialog', { name: 'Discard the interrupted checkpoint?' })
        await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
        expect(commands).toHaveLength(2)
        await page.getByRole('button', { name: 'Discard checkpoint', exact: true }).click()
        await page
          .getByRole('dialog')
          .getByRole('button', { name: 'Discard checkpoint', exact: true })
          .click()
        await expect.poll(() => commands.length).toBe(3)
        expect(commands[2]).toEqual({ command: 'discard-interrupted', confirm: true })
        await expect(
          page.getByRole('button', { name: 'Discard checkpoint', exact: true })
        ).toHaveCount(0)
        Object.assign(status, {
          busy: true,
          state: 'Starting',
          step_name: 'Mash',
          step_seconds: 120,
          current_step: 0,
          step_count: 3,
        })
        await page.getByRole('button', { name: 'Manage machine' }).click()
        await page.getByRole('button', { name: 'Test connection', exact: true }).click()
        await expect(page.getByRole('heading', { name: 'Current session' })).toBeVisible()
        await expect(page.getByRole('button', { name: 'Pause session' })).toBeDisabled()
        await expect(page.getByRole('button', { name: 'Resume session' })).toBeDisabled()
        status.state = 'Running'
        await page.reload()
        await expect(page.getByRole('button', { name: 'Pause session' })).toBeDisabled()
        await expect(page.getByRole('button', { name: 'Resume session' })).toBeDisabled()
        Object.assign(status, { can_pause: true, can_resume: false })
        await page.reload()
        await expect(page.getByRole('button', { name: 'Pause session' })).toBeEnabled()
        await expect(page.getByRole('button', { name: 'Resume session' })).toBeDisabled()
        Object.assign(status, { can_pause: false, can_resume: true })
        await page.reload()
        await expect(page.getByRole('button', { name: 'Pause session' })).toBeDisabled()
        await expect(page.getByRole('button', { name: 'Resume session' })).toBeEnabled()
        await expect(page.getByRole('button', { name: 'Open program' })).toHaveCount(0)
        const activeLock = page.getByRole('button', { name: 'Stop and lock outputs', exact: true })
        await expect(activeLock).toBeEnabled()
        await activeLock.click()
        await expect(
          page.getByRole('button', { name: 'Outputs locked', exact: true })
        ).toBeDisabled()
        expect(commands.at(-1)).toEqual({ command: 'stop' })
        await page.getByRole('button', { name: 'Unlock outputs', exact: true }).click()
        dialog = page.getByRole('dialog', { name: 'Unlock outputs?' })
        await dialog.getByRole('button', { name: 'Unlock outputs', exact: true }).click()
        await expect(idleLock).toBeEnabled()
        expect(commands.at(-1)).toEqual({ command: 'clear-stop', confirm: true })
        await idleLock.click()
        await expect(
          page.getByRole('button', { name: 'Outputs locked', exact: true })
        ).toBeDisabled()
        expect(commands.at(-1)).toEqual({ command: 'stop' })
        Object.assign(device, { status: null })
        await page.getByRole('button', { name: 'Manage machine' }).click()
        await page.getByRole('button', { name: 'Test connection', exact: true }).click()
        await expect(
          page.getByRole('heading', { name: 'Waiting for machine status' })
        ).toBeVisible()
        await expect(page.getByRole('button', { name: 'Open program' })).toHaveCount(0)
        await expect(activeLock).toBeEnabled()
        Object.assign(device, { status })
        Object.assign(device, { online: false })
        await page.getByRole('button', { name: 'Manage machine' }).click()
        await page.getByRole('button', { name: 'Test connection', exact: true }).click()
        await expect(page.getByRole('heading', { name: 'Machine offline' })).toBeVisible()
        await expect(activeLock).toBeDisabled()
        await expect(canvas).toBeHidden()
        await expect(page.getByText('LIVE OLED', { exact: false })).toHaveCount(0)
        await expect(page.getByRole('button', { name: 'Press dial', exact: true })).toBeDisabled()
        await expect(page.getByRole('region', { name: 'Live readings' })).toHaveCount(0)
        Object.assign(device, { online: true })
        await page.getByRole('button', { name: 'Manage machine' }).click()
        await page.getByRole('button', { name: 'Test connection', exact: true }).click()
        await expect(canvas).toBeVisible()
      } else {
        await expect(page.getByRole('button', { name: 'Press dial', exact: true })).toHaveCount(0)
        await expect(page.getByText('Thermoblock', { exact: true })).toHaveCount(0)
        await expect(page.getByRole('button', { name: 'Start recipe', exact: true })).toHaveCount(0)
        await expect(page.getByRole('region', { name: 'Cleaning and maintenance' })).toContainText(
          ferm ? 'gray fermentation seal' : 'condenser'
        )
        if (ferm) {
          await expect(page.getByLabel('Sampling interval (seconds)', { exact: true })).toHaveValue(
            '720'
          )
          await page.getByRole('button', { name: 'Calibrate pressure', exact: true }).click()
          let dialog = page.getByRole('dialog', { name: 'Calibrate pressure?' })
          await expect(dialog).toContainText('GRAY seal')
          await expect(dialog).toContainText('depressurized')
          await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
          expect(commands).toEqual([])
          await page.getByRole('button', { name: 'Calibrate pressure', exact: true }).click()
          dialog = page.getByRole('dialog', { name: 'Calibrate pressure?' })
          await dialog
            .getByRole('button', { name: 'Depressurized — calibrate', exact: true })
            .click()
          await expect(dialog).not.toBeVisible()
          expect(commands[0]).toEqual({ command: 'calibrate', confirm: true })
          await page.getByLabel('Sampling interval (seconds)', { exact: true }).fill('900')
          await page.getByRole('button', { name: 'Set sampling interval', exact: true }).click()
          await page
            .getByRole('dialog')
            .getByRole('button', { name: 'Set sampling interval', exact: true })
            .click()
          await expect.poll(() => commands.length).toBe(2)
          expect(commands[1]).toEqual({
            command: 'set-sampling-interval',
            value: 900,
            confirm: true,
          })
          Object.assign(status, { state: 'sensor-fault', ready: false })
          await page.reload()
          await expect(
            page.getByRole('button', { name: 'Read sensors now', exact: true })
          ).toBeEnabled()
          await expect(
            page.getByRole('button', { name: 'Start recording', exact: true })
          ).toBeDisabled()
          Object.assign(status, { state: 'recording', ready: true, busy: true })
          await page.reload()
          await expect(
            page.getByRole('button', { name: 'Set sampling interval', exact: true })
          ).toBeDisabled()
        } else {
          await expect(page.getByRole('button', { name: 'Set pump', exact: true })).toBeDisabled()
          Object.assign(status, { state: 'running', busy: true })
          await page.reload()
          await expect(page.getByLabel('Output level (%)', { exact: true })).toHaveValue('25')
          await expect(page.getByLabel('Fan enabled', { exact: true })).toBeChecked()
          await page.getByRole('button', { name: 'Set pump', exact: true }).click()
          await page
            .getByRole('dialog')
            .getByRole('button', { name: 'Set pump', exact: true })
            .click()
          await expect.poll(() => commands.length).toBe(1)
          expect(commands[0]).toEqual({ command: 'set-pump', enabled: false, confirm: true })
          await page.getByLabel('Fan', { exact: true }).selectOption('2')
          await expect(page.getByLabel('Output level (%)', { exact: true })).toHaveValue('0')
          await expect(page.getByLabel('Fan enabled', { exact: true })).not.toBeChecked()
          await page.getByLabel('Output level (%)', { exact: true }).fill('35')
          await expect(page.getByLabel('Fan enabled', { exact: true })).toBeChecked()
          await page.getByRole('button', { name: 'Set fan', exact: true }).click()
          await page
            .getByRole('dialog')
            .getByRole('button', { name: 'Set fan', exact: true })
            .click()
          await expect.poll(() => commands.length).toBe(2)
          expect(commands[1]).toEqual({
            command: 'set-fan',
            channel: 2,
            level: 35,
            enabled: true,
            confirm: true,
          })
          status.actuators!.find((item) => item.id === 'pump')!.enabled = true
          await page.reload()
          await page.getByLabel('Fan', { exact: true }).selectOption('2')
          const level = page.getByLabel('Output level (%)', { exact: true })
          await expect(level).toHaveAttribute('min', '61')
          await level.fill('60')
          await expect(page.getByRole('button', { name: 'Set fan', exact: true })).toBeDisabled()
          await level.fill('61')
          await expect(page.getByRole('button', { name: 'Set fan', exact: true })).toBeEnabled()
          await expect(page.getByLabel('Fan enabled', { exact: true })).toBeDisabled()
        }
        expect(reads.some((path) => /\/(display|recipes|ai-monitor)$/.test(path))).toBe(false)
      }
      await page.screenshot({
        path: test.info().outputPath(`${model}-${width}.png`),
        fullPage: true,
      })
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
      ).toBe(true)
      if (!brewing) {
        await page.getByRole('button', { name: 'Manage machine' }).click()
        await page.getByRole('button', { name: 'Device preferences', exact: true }).click()
        const dialog = page.getByRole('dialog', { name: 'Device preferences' })
        await expect(dialog.getByLabel('Temperature unit', { exact: true })).toBeVisible()
        await expect(dialog.getByLabel('Front-panel language', { exact: true })).toHaveCount(0)
        await expect(dialog.getByLabel('Show detailed status', { exact: true })).toHaveCount(0)
        await dialog.getByRole('button', { name: 'Close dialog' }).click()
      }
      await page.goto('/#/history/family-device-1')
      await page.getByLabel('Recorded measurements', { exact: true }).selectOption('raw')
      await expect(page.getByRole('button', { name: 'Native pressure', exact: true })).toBeVisible()
      await page.getByRole('button', { name: 'Show raw samples' }).click()
      await expect(page.getByRole('columnheader', { name: 'Native pressure (raw)' })).toBeVisible()
      expect(errors).toEqual([])
    })
  }
}
