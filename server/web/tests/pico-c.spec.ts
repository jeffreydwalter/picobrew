import { test, expect } from '@playwright/test'
import type { Device, Display, FirmwarePackage, MachineFirmware } from '../src/types'

for (const viewport of [
  { name: 'desktop', width: 1280, height: 900 },
  { name: 'mobile', width: 390, height: 844 },
]) {
  for (const controller of ['ssd1325', 'ssd1309'] as const) {
    test(`Pico C ${controller} screen and USB-only firmware on ${viewport.name}`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport)
      const errors: string[] = []
      page.on('pageerror', (error) => errors.push(error.message))
      const repository = 'jeffreydwalter/picobrew'
      const commit = 'a'.repeat(40)
      const uid = 'b'.repeat(32)
      const other = controller === 'ssd1325' ? 'ssd1309' : 'ssd1325'
      const height = controller === 'ssd1325' ? 64 : 32
      const choices: FirmwarePackage[] = ['ssd1325', 'ssd1309'].map((display_variant, i) => ({
        id: String(i + 1).repeat(64),
        model: 'pico-c',
        model_family: 'pico-c',
        display_variant,
        firmware_version: '1.2.0',
        binary_sha256: String(i + 3).repeat(64),
        binary_bytes: i === 0 ? 162152 : 162064,
        package_bytes: 223680,
        created_at: '2026-11-20T00:00:00Z',
        validation: 'passed',
        provenance: { source: 'github', repository, commit },
      }))
      let verified = ''
      let rejectObservation = true
      const observations: unknown[] = []
      const commands: unknown[] = []
      let waitingForFrame = false
      let releaseFrame!: () => void
      const nextFrame = new Promise<void>((resolve) => {
        releaseFrame = resolve
      })
      const identity = {
        model: 'pico-c',
        model_name: 'Pico C',
        model_family: 'pico-c',
        display_variant: controller,
      }
      const machine: Device = {
        id: 'browser-pico-c',
        name: 'Kitchen Pico C',
        model: 'pico-c',
        model_source: 'firmware',
        adapter: 'pico-standalone',
        enabled: true,
        online: true,
        base_url: 'http://192.168.1.43',
        location: 'Kitchen',
        key_configured: true,
        created_at: '',
        last_seen: null,
        status: {
          ...identity,
          uptime_ms: 60000,
          ready: true,
          device_uid: uid,
          busy: false,
          stopped: false,
          command: 'idle',
          device_state: 0,
          phase: 0,
          controller_status: 0,
          compartment: 0,
          wort_f: 72.4,
          thermoblock_f: 73,
          target_f: 0,
          step_seconds: 0,
          current_step: 0,
          step_count: 0,
          recipe_name: '',
          step_name: '',
          session_id: '',
          filter_inserted: true,
          stop_reason: 'none',
          encoder: { position: 0, min: 0, max: 3 },
        },
        capabilities: {
          ...identity,
          api_version: 1,
          firmware_version: '1.2.0',
          commands: ['encoder', 'stop'],
          recipe_capacity: 16,
          step_capacity: 15,
          front_panel: true,
          history_persistent: true,
          telemetry_forwarding: true,
          dedicated_pause_resume: false,
        },
      }
      // Independent raw page/bit landmarks distinguish rotation from reflections.
      // Nonzero hidden SSD1309 pages must not become visible pixels.
      const raw = Buffer.alloc(1024)
      raw[0] = 1
      raw[130] = 4
      raw[511] = 128
      if (controller === 'ssd1309') raw.fill(255, 512)
      else raw[1023] = 128
      const frame: Display = {
        width: 128,
        height,
        rotation: controller === 'ssd1325' ? 2 : 0,
        display_variant: controller,
        storage_bytes: 1024,
        transfer_bytes: 1024,
        format: 'page-lsb-hex',
        data: raw.toString('hex'),
      }
      const conflict =
        'The firmware-reported display differs from the independently verified controller.'
      const changed = 'The machine identity changed; reload before recording its controller'
      const machineFirmware = (): MachineFirmware => ({
        device_id: machine.id,
        device_uid: uid,
        model: 'pico-c',
        current_version: '1.2.0',
        display_variant: controller,
        verified_display_variant: verified,
        hardware_verification_required: verified !== controller,
        install_supported: false,
        reason:
          verified && verified !== controller
            ? conflict
            : 'Network installation is not available yet.',
        packages:
          verified === controller
            ? choices.filter((item) => item.display_variant === controller)
            : [],
      })
      await page.route('**/api/v1/events', (route) =>
        route.fulfill({ contentType: 'text/event-stream', body: ': isolated C fixture\n\n' })
      )
      await page.route('**/api/v1/firmware/**', async (route) => {
        const request = route.request()
        const path = new URL(request.url()).pathname
        if (path.endsWith('/settings')) {
          await route.fulfill({ json: { repository, ref: '' } })
        } else if (path.endsWith('/updates')) {
          await route.fulfill({
            json: {
              releases: choices.map((p) => ({ ...p, archive_sha256: p.id, kind: 'standalone' })),
              notices: [],
            },
          })
        } else throw new Error(`Unexpected firmware request: ${request.method()} ${path}`)
      })
      await page.route('**/api/v1/devices**', async (route) => {
        const request = route.request()
        const path = new URL(request.url()).pathname
        let result: unknown
        if (path === '/api/v1/devices') result = [machine]
        else if (path.endsWith('/brew-invite')) result = { invite: null }
        else if (path.endsWith('/display')) {
          if (commands.length) {
            waitingForFrame = true
            await nextFrame
          }
          result = frame
        } else if (path.endsWith('/recipes')) result = []
        else if (path.endsWith('/ai-monitor'))
          result = { enabled: false, state: 'off', configured: false }
        else if (path.endsWith('/firmware/hardware')) {
          const observation = request.postDataJSON()
          expect(request.method()).toBe('PUT')
          expect(request.headers()['x-csrf-token']).toBeTruthy()
          expect(observation.device_uid).toBe(uid)
          observations.push(observation)
          // The server can reject a stale identity after the dialog was loaded.
          // That rejection must not optimistically expose a matching package.
          if (rejectObservation) {
            rejectObservation = false
            await route.fulfill({
              status: 409,
              json: { error: { code: 'firmware_hardware', message: changed } },
            })
            return
          }
          verified = observation.display_variant
          result = { ...observation, verified_at: '2026-11-20T00:00:00Z' }
        } else if (path.endsWith('/firmware')) result = machineFirmware()
        else if (path.endsWith('/commands')) {
          expect(request.method()).toBe('POST')
          expect(request.headers()['idempotency-key']).toBeTruthy()
          commands.push(request.postDataJSON())
          result = { accepted: true }
        } else throw new Error(`Unexpected machine request: ${request.method()} ${path}`)
        await route.fulfill({ json: result })
      })

      await page.goto(`/#/devices/${machine.id}`)
      await expect(page.getByRole('heading', { name: machine.name, exact: true })).toBeVisible()
      const frontPanel = page.locator('.front-panel')
      const canvas = frontPanel.getByLabel('Live Pico OLED display')
      await expect(canvas).toHaveAttribute('width', '128')
      await expect(canvas).toHaveAttribute('height', String(height))
      await expect(
        frontPanel.getByText(`LIVE OLED · 128 × ${height}`, { exact: true })
      ).toBeVisible()
      await expect
        .poll(() =>
          canvas.evaluate((element: HTMLCanvasElement) => {
            const pixels = element
              .getContext('2d')!
              .getImageData(0, 0, element.width, element.height).data
            const lit: number[][] = []
            for (let y = 0; y < element.height; y++)
              for (let x = 0; x < element.width; x++) {
                const at = (y * element.width + x) * 4
                if (pixels[at] === 196 && pixels[at + 1] === 219 && pixels[at + 2] === 171)
                  lit.push([x, y])
              }
            return lit
          })
        )
        .toEqual(
          controller === 'ssd1325'
            ? [
                [0, 0],
                [0, 32],
                [125, 53],
                [127, 63],
              ]
            : [
                [0, 0],
                [2, 10],
                [127, 31],
              ]
        )
      const bounds = await canvas.boundingBox()
      expect(bounds!.width / bounds!.height).toBeCloseTo(128 / height, 2)
      await frontPanel.screenshot({
        path: test.info().outputPath(`c-screen-${controller}-${viewport.name}.png`),
      })
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
      ).toBe(true)
      // Ordinary front-panel control does not require firmware hardware inventory.
      const panelLayout = () =>
        frontPanel.evaluate((panel) => {
          const oled = panel.querySelector('.oled')!.getBoundingClientRect()
          const dial = panel.querySelector('.dial-controls')!.getBoundingClientRect()
          return { height: oled.height, dialOffset: dial.top - oled.top }
        })
      const layoutBefore = await panelLayout()
      const pixelsBefore = await canvas.evaluate((c: HTMLCanvasElement) => c.toDataURL())
      const encoderReply = page.waitForResponse((response) =>
        response.url().endsWith(`/devices/${machine.id}/commands`)
      )
      await frontPanel.getByRole('button', { name: 'Press dial', exact: true }).click()
      await encoderReply
      try {
        await expect.poll(() => waitingForFrame).toBe(true)
        await expect(canvas).toBeVisible()
        expect(await canvas.evaluate((c: HTMLCanvasElement) => c.toDataURL())).toBe(pixelsBefore)
        expect(await panelLayout()).toEqual(layoutBefore)
      } finally {
        releaseFrame()
      }
      await expect(
        frontPanel.getByRole('button', { name: 'Press dial', exact: true })
      ).toBeEnabled()
      expect(commands).toEqual([{ command: 'encoder', click: true }])
      machine.status!.ready = false
      machine.capabilities!.commands.push('restart')
      await page.reload()
      await expect(
        frontPanel.getByRole('button', { name: 'Press dial', exact: true })
      ).toBeDisabled()
      await expect(
        page.getByRole('button', { name: 'Stop and lock outputs', exact: true })
      ).toBeDisabled()
      machine.status!.ready = true
      machine.last_seen = new Date().toISOString()
      await page.reload()
      await page.getByRole('button', { name: 'Manage machine' }).click()
      await expect(page.getByRole('button', { name: 'Restart brewer' })).toBeDisabled()
      machine.status!.phase = 4
      await page.reload()
      await page.getByRole('button', { name: 'Manage machine' }).click()
      await page.getByRole('button', { name: 'Restart brewer' }).click()
      await page
        .getByRole('dialog', { name: 'Restart brewer?' })
        .getByRole('button', { name: 'Restart brewer' })
        .click()
      await expect(page.getByRole('dialog')).toContainText('Restart acknowledged')
      expect(commands.at(-1)).toEqual({ command: 'restart', confirm: true })
      await page.getByRole('button', { name: 'Close dialog', exact: true }).click()

      await page.getByRole('button', { name: 'Firmware 1.2.0', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: 'Machine firmware', exact: true })
      const select = dialog.getByLabel('Verified display controller')
      const check = dialog.getByRole('checkbox', {
        name: /I checked this machine's display controller independently/,
      })
      const save = dialog.getByRole('button', { name: 'Save verified controller', exact: true })
      await expect(select).toHaveValue('')
      await expect(check).toBeDisabled()
      await expect(save).toBeDisabled()
      await expect(dialog.getByLabel('Firmware version')).toBeDisabled()
      await select.selectOption(other)
      await expect(save).toBeDisabled()
      await check.check()
      await save.click()
      await expect(dialog.getByRole('alert')).toContainText(changed)
      await expect(select).toHaveValue(other)
      await expect(dialog.getByLabel('Firmware version')).toBeDisabled()
      expect(verified).toBe('')

      // A recorded controller that contradicts the running build is also blocked.
      await save.click()
      await expect(dialog.getByRole('status')).toHaveText(conflict)
      await expect(
        dialog.getByText(`Verified hardware: ${other.toUpperCase()}`, { exact: true })
      ).toBeVisible()
      await expect(dialog.getByLabel('Firmware version')).toBeDisabled()
      await expect(select).toHaveValue('')
      await expect(check).not.toBeChecked()
      await expect(save).toBeDisabled()
      await select.selectOption(controller)
      await check.check()
      await save.click()
      const matching = dialog.getByLabel('Firmware version')
      await expect(matching).toBeDisabled()
      await expect(matching.locator('option')).toHaveText(
        'No compatible wireless firmware available'
      )
      await expect(dialog.getByRole('button', { name: /^Install/ })).toHaveCount(0)
      expect(observations).toEqual(
        [other, other, controller].map((display_variant) => ({ device_uid: uid, display_variant }))
      )
      expect(commands).toEqual([
        { command: 'encoder', click: true },
        { command: 'restart', confirm: true },
      ])
      await dialog.screenshot({
        path: test.info().outputPath(`c-verified-${controller}-${viewport.name}.png`),
      })
      await expect(dialog.getByRole('button', { name: 'Download', exact: true })).toBeDisabled()
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
      ).toBe(true)

      await page.reload()
      await page.getByRole('button', { name: 'Firmware 1.2.0', exact: true }).click()
      await expect(matching).toBeVisible()
      await expect(matching.locator('option')).toHaveCount(1)
      await expect(select).not.toBeVisible()
      expect(observations).toHaveLength(3)
      expect(commands).toEqual([
        { command: 'encoder', click: true },
        { command: 'restart', confirm: true },
      ])
      expect(errors).toEqual([])
    })
  }
}
