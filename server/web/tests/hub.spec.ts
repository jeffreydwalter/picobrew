import { test, expect } from '@playwright/test'

test.beforeEach(async ({ request }) => {
  const response = await request.get('/api/v1/auth/session')
  expect(
    (await response.json()).demo,
    'Browser mutation tests require an isolated simulation workspace'
  ).toBe(true)
})

test('fleet opens a machine and exposes real front-panel controls', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.goto('/#/overview')
  await expect(page.getByRole('heading', { name: 'Brewery' })).toBeVisible()
  await expect(page.locator('.machine-card')).toHaveCount(3)
  await expect(page.getByText('SIMULATION', { exact: true })).toBeVisible()
  await page.screenshot({ path: test.info().outputPath('overview-desktop.png'), fullPage: true })
  await page
    .locator('.machine-card')
    .first()
    .getByRole('link', { name: /^Open .+ controls$/ })
    .click()
  await expect(page.getByRole('heading', { name: 'Simulator controls' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Move selection up' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Move selection down' })).toBeVisible()
  const oled = page.getByLabel('Simulated Pico OLED display')
  await expect(oled).toBeVisible()
  const pixels = () => oled.evaluate((e) => (e as HTMLCanvasElement).toDataURL())
  const initial = await pixels()
  await expect(page.getByRole('button', { name: 'Press dial' })).toBeEnabled()
  await page.getByRole('button', { name: 'Move selection down' }).click()
  await expect.poll(pixels).not.toBe(initial)
  await expect(page.getByRole('button', { name: 'Press dial' })).toBeEnabled()
  expect(errors).toEqual([])
})

test('machine management stays on the Overview and works offline on mobile', async ({
  page,
  request,
}) => {
  const longName = 'Mobile brewing bench with a long machine name for the shared family workspace'
  let id = ''
  try {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/#/overview')
    await page.getByRole('button', { name: 'Add machine', exact: true }).click()
    let dialog = page.getByRole('dialog', { name: 'Add machine', exact: true })
    await expect(dialog).toBeVisible()
    await expect(page).toHaveURL(/#\/overview$/)
    await dialog.getByLabel('Machine name').fill(longName)
    await dialog.getByLabel('Location').fill('Upstairs brew studio')
    await page.screenshot({
      path: test.info().outputPath('add-machine-mobile.png'),
      fullPage: true,
    })
    await dialog.getByRole('button', { name: 'Add machine', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    const created = (await (await request.get('/api/v1/devices')).json()).find(
      (device: { name: string }) => device.name === longName
    )
    expect(created.adapter).toBe('simulator')
    id = created.id
    let card = page
      .locator('.machine-card')
      .filter({ has: page.getByRole('heading', { name: longName, exact: true }) })
    await expect(card).toBeVisible()
    const bounds = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      width: innerWidth,
    }))
    expect(bounds.scroll).toBeLessThanOrEqual(bounds.width + 1)
    await page.screenshot({
      path: test.info().outputPath('machine-actions-mobile.png'),
      fullPage: true,
    })

    await card.getByRole('button', { name: `Edit ${longName}`, exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Edit machine connection' })
    await expect(dialog).toBeVisible()
    await expect(page).toHaveURL(/#\/overview$/)
    await expect(dialog.getByLabel('Machine model')).toBeDisabled()
    await expect(dialog.getByLabel('Connection type')).toBeDisabled()
    await dialog.getByLabel('Machine name').fill('Browser managed brewer')
    await dialog.getByLabel('Monitor this machine').uncheck()
    await dialog.getByRole('button', { name: 'Save machine' }).click()
    await expect(dialog).not.toBeVisible()
    card = page
      .locator('.machine-card')
      .filter({ has: page.getByRole('heading', { name: 'Browser managed brewer', exact: true }) })
    const controls = card.getByRole('link', { name: 'Open Browser managed brewer controls' })
    await controls.focus()
    await controls.press('Enter')
    await expect(
      page.getByRole('heading', { name: 'Browser managed brewer', exact: true, level: 1 })
    ).toBeVisible()
    await expect(page.getByRole('button', { name: 'Edit connection' })).toBeHidden()
    await page.getByRole('button', { name: 'Manage machine' }).click()
    await expect(page.getByRole('dialog', { name: 'Machine management' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Edit connection' })).toBeEnabled()
    await expect(page.getByRole('button', { name: 'Remove machine', exact: true })).toBeEnabled()
    await expect(page.getByRole('button', { name: 'Brewer preferences' })).toBeDisabled()
    await page.getByRole('button', { name: 'Edit connection' }).click()
    dialog = page.getByRole('dialog', { name: 'Edit machine connection' })
    await dialog.getByLabel('Location').fill('Updated while offline')
    await dialog.getByRole('button', { name: 'Save machine' }).click()
    await expect(dialog).not.toBeVisible()
    expect(await (await request.get(`/api/v1/devices/${id}`)).json()).toMatchObject({
      enabled: false,
      online: false,
      location: 'Updated while offline',
    })

    await page.getByRole('button', { name: 'Manage machine' }).click()
    await page.getByRole('button', { name: 'Remove machine', exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Remove Browser managed brewer?' })
    await expect(dialog.getByText(/brew history/i)).toBeVisible()
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).not.toBeVisible()
    expect((await request.get(`/api/v1/devices/${id}`)).status()).toBe(200)
    await page.goto('/#/overview')
    await page.getByRole('button', { name: 'Remove Browser managed brewer', exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Remove Browser managed brewer?' })
    await dialog.getByRole('button', { name: 'Remove machine', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    expect((await request.get(`/api/v1/devices/${id}`)).status()).toBe(404)
    await expect(
      page.getByRole('link', { name: 'Open Browser managed brewer controls' })
    ).toHaveCount(0)

    await page.goto('/#/settings/new-device')
    dialog = page.getByRole('dialog', { name: 'Add machine', exact: true })
    await expect(dialog).toBeVisible()
    await expect(page).toHaveURL(/#\/overview\/new-device$/)
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).not.toBeVisible()
    await expect(page).toHaveURL(/#\/overview$/)
  } finally {
    if (id) await request.delete(`/api/v1/devices/${id}`, { headers: { 'X-CSRF-Token': 'demo' } })
  }
})

test('create, revise and import a recipe through the working editor', async ({ page }) => {
  await page.goto('/#/recipes/new?mode=batch')
  await expect(page.getByRole('heading', { name: 'What will you brew next?' })).toBeVisible()
  await page.getByLabel('Recipe name', { exact: true }).fill('Browser test porter')
  await page.getByLabel('Style', { exact: true }).fill('Porter')
  await page.getByLabel('Tags', { exact: true }).fill('browser, porter')
  await page.getByRole('button', { name: 'Add grain' }).click()
  await page.getByLabel('Ingredient 1 name').fill('Pale malt')
  await page.getByRole('button', { name: 'Add hops' }).click()
  await page.getByLabel('Ingredient 2 name').fill('Cascade')
  await page.getByLabel('Minutes before boil ends').fill('12.5')
  await page.getByLabel('New step type').selectOption('mash-out')
  await page.getByRole('button', { name: 'Add step', exact: true }).click()
  const mashOut = page.locator('.plan-row').filter({ has: page.getByLabel('3. Step name') })
  await expect(page.getByRole('button', { name: 'Remove ingredient' }).first()).toBeVisible()
  await expect(page.getByLabel('Boil volume (L)')).toBeVisible()
  await mashOut.getByLabel('Flow path', { exact: true }).selectOption('3')
  await mashOut.getByLabel('Drain after step (min)').fill('2')
  await page.getByRole('button', { name: 'Save recipe', exact: true }).click()
  await expect(
    page.getByRole('heading', { name: 'Browser test porter', exact: true, level: 1 })
  ).toBeVisible()
  await expect(page.getByText('Recipe saved to your library.')).toBeVisible()
  await page.getByLabel('Recipe notes').fill('Integration note: retained after save.')
  await page.getByRole('button', { name: 'Save recipe', exact: true }).click()
  await page.reload()
  await expect(page.getByLabel('Recipe notes')).toHaveValue(
    'Integration note: retained after save.'
  )
  await expect(page.getByLabel('Minutes before boil ends')).toHaveValue('12.5')
  await expect(mashOut.getByLabel('Flow path', { exact: true })).toHaveValue('3')
  await expect(mashOut.getByLabel('Drain after step (min)')).toHaveValue('2')
  await expect(page.getByLabel('Tags', { exact: true })).toHaveValue('browser, porter')
  await page.getByRole('button', { name: 'Revision history', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Recipe revision history' })).toBeVisible()
  await expect(page.getByText('Revision 1', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Close dialog' }).click()
  await page.goto('/#/recipes')
  await page.getByText('Add recipe', { exact: true }).click()
  await page.getByRole('button', { name: 'Import recipe', exact: true }).click()
  await page.getByLabel('Format', { exact: true }).selectOption('json')
  await page.getByLabel('Or paste the recipe').fill(
    JSON.stringify({
      name: 'Imported browser recipe',
      style: 'Pale ale',
      batch_liters: 5,
      efficiency: 55,
      ingredients: [
        {
          name: 'Pale malt',
          kind: 'fermentable',
          amount: 1.2,
          unit: 'kg',
          use: 'mash',
          minutes: 0,
        },
      ],
      steps: [
        { name: 'Mash', kind: 'mash', target_c: 67, minutes: 60, drain_minutes: 0, location: 1 },
      ],
      tags: [],
      notes: '',
    })
  )
  await page.getByRole('button', { name: 'Import recipes', exact: true }).click()
  await expect(
    page.getByRole('heading', { name: 'Imported browser recipe', exact: true, level: 1 })
  ).toBeVisible()
})

test('brew journal saves notes, exposes all sensor columns and exports', async ({ page }) => {
  await page.goto('/#/history/demo-history-demo-pro')
  await expect(page.getByRole('heading', { name: 'Session measurements' })).toBeVisible()
  await expect(page.getByRole('slider', { name: 'Inspect recorded samples' })).toBeVisible()
  await page
    .getByLabel('Brew notes', { exact: true })
    .fill('[25:30] Browser test annotation\nRoast aroma and stable mash.')
  await page.getByRole('button', { name: 'Rate 5 out of 5' }).click()
  await page.getByRole('button', { name: 'Save notes', exact: true }).click()
  await expect(page.getByText('Brew notes saved.')).toBeVisible()
  await page.reload()
  await expect(page.getByLabel('Brew notes', { exact: true })).toHaveValue(
    '[25:30] Browser test annotation\nRoast aroma and stable mash.'
  )
  await expect(page.getByRole('button', { name: 'Rate 5 out of 5' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await page.screenshot({ path: test.info().outputPath('history-desktop.png'), fullPage: true })
  await page.getByRole('button', { name: 'Show raw samples' }).click()
  await expect(page.getByRole('columnheader', { name: 'Flags', exact: true })).toBeVisible()
  const downloaded = page.waitForEvent('download')
  await page.getByRole('button', { name: 'CSV', exact: true }).click()
  expect((await downloaded).suggestedFilename()).toMatch(/\.csv$/)
})

test('settings, API explorer and contextual assistant are functional', async ({ page }) => {
  await page.goto('/#/settings')
  await expect(page.getByRole('heading', { name: 'Hub settings' })).toBeVisible()
  for (const name of [
    'Equipment calibration',
    'Display preferences',
    'Brew assistant',
    'Firmware',
    'Your data',
  ]) {
    await expect(page.getByRole('heading', { name, level: 2, exact: true })).toBeVisible()
  }
  await expect(page.getByRole('heading', { name: 'Your machines' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Add machine' })).toHaveCount(0)
  await page.screenshot({ path: test.info().outputPath('settings-desktop.png'), fullPage: true })
  await page.getByLabel('Temperature unit', { exact: true }).selectOption('C')
  await page.reload()
  await expect(page.getByLabel('Temperature unit', { exact: true })).toHaveValue('C')
  await page.getByRole('link', { name: 'API explorer', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Explore the hub API.' })).toBeVisible()
  await expect(page.locator('.endpoint-list>button')).not.toHaveCount(0)
  await page.getByRole('button', { name: 'Send request', exact: true }).click()
  await expect(page.locator('.api-response pre')).toContainText('demo-pro')
  await page.goto('/#/assistant?brew=demo-history-demo-pro')
  await expect(page).toHaveURL(/#\/history\/demo-history-demo-pro\?analysis=1$/)
  await expect(page.getByRole('heading', { name: 'AI brew analysis' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Session measurements' })).toBeVisible()
  await expect(page.getByRole('tablist', { name: 'Assistant task' })).toHaveCount(0)
  await expect(page.getByLabel('Brew to analyze')).toHaveCount(0)
})

test('mobile layouts contain wide recipe and history tables', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  for (const route of [
    '/overview',
    '/recipes/studio-porter',
    '/history/demo-history-demo-pro',
    '/settings',
    '/api',
  ]) {
    await page.goto('/#' + route)
    await expect(page.locator('h1')).toBeVisible()
    await page.waitForTimeout(250)
    const bounds = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      width: innerWidth,
    }))
    expect(bounds.scroll, `${route} overflows`).toBeLessThanOrEqual(bounds.width + 1)
    if (route === '/overview' || route === '/settings')
      await page.screenshot({
        path: test.info().outputPath(route.slice(1) + '-mobile.png'),
        fullPage: true,
      })
  }
  await page.getByRole('button', { name: 'Open navigation' }).click()
  await expect(page.locator('.sidebar')).toHaveClass(/open/)
  await page
    .getByRole('navigation', { name: 'Main navigation' })
    .getByRole('link', { name: /^Recipes/ })
    .click()
  await expect(page.getByRole('heading', { name: 'Recipe library' })).toBeVisible()
})

test('community catalog preserves source schedules through copy, bulk transfer and machine editing', async ({
  page,
  request,
}) => {
  test.setTimeout(60000)
  const headers = { 'X-CSRF-Token': 'demo' }
  let deviceID = ''
  let recipeID = ''
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  try {
    const created = await request.post('/api/v1/devices', {
      headers,
      data: {
        name: 'Catalog transfer brewer',
        adapter: 'simulator',
        model: 'pico-pro',
        enabled: true,
      },
    })
    expect(created.ok()).toBe(true)
    deviceID = (await created.json()).id
    await request.post(`/api/v1/devices/${deviceID}/test`, { headers, data: {} })
    const original = await (await request.get('/api/v1/catalog/picopak-64')).json()
    expect(
      original.native_program.steps.some((step: { minutes: number }) => step.minutes === 0)
    ).toBe(true)
    const before = await (await request.get(`/api/v1/devices/${deviceID}/recipes`)).json()

    await page.goto('/#/catalog')
    await expect(page.getByRole('heading', { name: 'Community recipes' })).toBeVisible()
    await expect(page.getByRole('option', { name: 'Machine schedules' })).toHaveCount(0)
    await page.getByRole('button', { name: 'Sources & credits' }).click()
    await page.getByRole('button', { name: 'Browse source archive' }).click()
    await page.getByLabel('Archive record type', { exact: true }).selectOption('native-program')
    await page.getByLabel('Search community recipes').fill('Pico Pale')
    await expect(
      page
        .locator('.catalog-card')
        .filter({ has: page.getByRole('button', { name: 'Pico Pale', exact: true }) })
    ).toHaveCount(1)
    await page.screenshot({
      path: test.info().outputPath('community-catalog-desktop.png'),
      fullPage: true,
    })
    await page.getByRole('button', { name: 'Pico Pale', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'The original machine schedule' })).toBeVisible()
    await expect(
      page.getByRole('cell', { name: 'Reach target', exact: true }).first()
    ).toBeVisible()
    await page.getByRole('button', { name: 'Copy to my library', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Save program', exact: true })).toBeVisible()
    recipeID = page.url().split('/recipes/')[1]
    expect(recipeID).toBeTruthy()
    await expect(page.getByLabel('Batch size (L)', { exact: true })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Add grain', exact: true })).toHaveCount(0)
    await expect(page.getByRole('heading', { name: 'Z PicoPak adapter' })).toBeVisible()
    await expect(page.getByRole('cell', { name: 'Reach target', exact: true }).first()).toBeVisible()
    await expect(page.getByRole('button', { name: 'Edit as a standard Pico program' })).toBeVisible()
    await page
      .getByLabel('Program notes', { exact: true })
      .fill('Copied source schedule, reviewed in browser.')
    await page.getByRole('button', { name: 'Save program', exact: true }).click()
    await expect(page.getByText('PicoPak saved to your library.')).toBeVisible()
    const saved = await (await request.get(`/api/v1/recipes/${recipeID}`)).json()
    expect(saved.native_program).toEqual(original.native_program)
    expect(saved.batch_liters).toBe(0)
    expect(saved.ingredients).toEqual([])
    expect(saved.provenance.catalog_id).toBe('picopak-64')

    await page.getByRole('button', { name: 'Send', exact: true }).click()
    let dialog = page.getByRole('dialog', { name: 'Send recipes & programs', exact: true })
    await dialog.getByLabel('Transfer to machine', { exact: true }).selectOption(deviceID)
    await dialog.getByRole('button', { name: 'Preview transfer', exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Review this transfer', exact: true })
    await expect(dialog.getByText(`${before.recipes.length} / 16`, { exact: true })).toBeVisible()
    await expect(dialog.getByText('Ready to send', { exact: true })).toBeVisible()
    await dialog.locator('summary').filter({ hasText: 'Review machine schedule' }).click()
    await page.screenshot({
      path: test.info().outputPath('transfer-preview-desktop.png'),
      fullPage: true,
    })
    await dialog.getByRole('button', { name: 'Send 1 program', exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Transfer receipt', exact: true })
    await expect(dialog.getByText('Verified on machine', { exact: true })).toBeVisible()
    await expect(dialog.getByText('Transfer complete.', { exact: true })).toBeVisible()
    const after = await (await request.get(`/api/v1/devices/${deviceID}/recipes`)).json()
    expect(
      after.recipes.find((program: { id: string }) => program.id === original.native_program.id)
    ).toEqual({
      id: original.native_program.id,
      name: original.native_program.name,
      steps: original.native_program.steps,
    })
    for (const existing of before.recipes) expect(after.recipes).toContainEqual(existing)
    const device = await (await request.get(`/api/v1/devices/${deviceID}`)).json()
    expect(device.status.busy).toBe(false)
    await dialog.getByRole('button', { name: 'Done', exact: true }).click()

    await page.goto(`/#/devices/${deviceID}`)
    await page
      .getByLabel('Recipe on this machine', { exact: true })
      .selectOption(original.native_program.id)
    await expect(page.getByText(/recipe slots used/)).toBeVisible()
    await page.getByRole('button', { name: 'Edit saved program', exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Edit the machine’s saved program', exact: true })
    const programName = dialog.getByLabel('Saved program name', { exact: true })
    await programName.fill('A🍺')
    await dialog.getByRole('button', { name: 'Save to machine', exact: true }).click()
    await expect(dialog.getByRole('alert')).toBeVisible()
    await expect(programName).toBeEditable()
    await expect(programName).toHaveValue('A🍺')
    await programName.fill('Pico Pale bench edit')
    await dialog.getByRole('button', { name: 'Save to machine', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    const changed = await (await request.get(`/api/v1/devices/${deviceID}/recipes`)).json()
    expect(
      changed.recipes.find((program: { id: string }) => program.id === original.native_program.id)
    ).toMatchObject({ name: 'Pico Pale bench edit', steps: original.native_program.steps })
    expect((await (await request.get(`/api/v1/recipes/${recipeID}`)).json()).name).toBe('Pico Pale')

    // A second preview preserves the machine edit unless the user elects to update matching IDs.
    await page.goto(`/#/recipes/${recipeID}`)
    await page.getByRole('button', { name: 'Send', exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Send recipes & programs', exact: true })
    await dialog.getByLabel('Transfer to machine', { exact: true }).selectOption(deviceID)
    await dialog.getByRole('button', { name: 'Preview transfer', exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Review this transfer', exact: true })
    await expect(dialog.getByText('Skipped', { exact: true })).toBeVisible()
    await expect(dialog.getByText(/enable updating matching recipes/)).toBeVisible()
    await expect(
      dialog.getByRole('button', { name: 'Send 0 programs', exact: true })
    ).toBeDisabled()
    expect(errors).toEqual([])
  } finally {
    if (deviceID) await request.delete(`/api/v1/devices/${deviceID}`, { headers })
    if (recipeID) await request.delete(`/api/v1/recipes/${recipeID}`, { headers })
  }
})

test('catalog images, machine guides and program editor fit a mobile screen', async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  const externalImages: string[] = []
  page.on('request', (req) => {
    if (req.resourceType() === 'image' && !req.url().startsWith(new URL(page.url()).origin))
      externalImages.push(req.url())
  })
  await page.goto('/#/catalog?kind=recipe')
  await expect(page.locator('.catalog-card').first()).toBeVisible()
  const image = page.locator('.catalog-card img').first()
  await expect(image).toBeVisible()
  await expect
    .poll(() => image.evaluate((node: HTMLImageElement) => node.naturalWidth))
    .toBeGreaterThan(0)
  await page.getByRole('button', { name: 'Machine guides & artwork', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Machine guides & artwork', exact: true })
  await expect(dialog.locator('.machine-artwork img').first()).toBeVisible()
  await page.screenshot({
    path: test.info().outputPath('machine-guides-mobile.png'),
    fullPage: true,
  })
  await dialog.getByRole('button', { name: 'Close dialog', exact: true }).click()
  for (const route of ['/catalog', '/catalog/picopak-64', '/catalog?kind=maintenance']) {
    await page.goto('/#' + route)
    await expect(page.locator('h1')).toBeVisible()
    const bounds = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      width: innerWidth,
    }))
    expect(bounds.scroll, `${route} overflows`).toBeLessThanOrEqual(bounds.width + 1)
  }
  const catalog = await (await request.get('/api/v1/catalog?kind=maintenance')).json()
  expect(catalog.items.length).toBeGreaterThan(0)
  await expect(page.getByText('Cleaning program', { exact: true }).first()).toBeVisible()
  await page.screenshot({
    path: test.info().outputPath('cleaning-catalog-mobile.png'),
    fullPage: true,
  })
  const copied = await request.post('/api/v1/catalog/picopak-64/copy', {
    headers: { 'X-CSRF-Token': 'demo' },
    data: {},
  })
  expect(copied.ok()).toBe(true)
  const recipeID = (await copied.json()).id
  try {
    await page.goto(`/#/recipes/${recipeID}`)
    await expect(page.getByRole('button', { name: 'Save program', exact: true })).toBeVisible()
    const bounds = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      width: innerWidth,
    }))
    expect(bounds.scroll, 'Native program editor overflows').toBeLessThanOrEqual(bounds.width + 1)
    await page.screenshot({
      path: test.info().outputPath('native-program-editor-mobile.png'),
      fullPage: true,
    })
  } finally {
    await request.delete(`/api/v1/recipes/${recipeID}`, { headers: { 'X-CSRF-Token': 'demo' } })
  }
  expect(externalImages).toEqual([])
  expect(errors).toEqual([])
})

test('appearance follows the system, remembers overrides, and applies to the machine panel', async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.goto('/#/settings')
  const appearance = page.getByLabel('Appearance', { exact: true })
  const background = () =>
    page.locator('html').evaluate((el) => getComputedStyle(el).backgroundColor)
  await expect(appearance).toHaveValue('system')
  const dark = await background()
  await page.emulateMedia({ colorScheme: 'light' })
  await expect.poll(background).not.toBe(dark)
  const light = await background()
  await appearance.selectOption('dark')
  await page.reload()
  await expect(appearance).toHaveValue('dark')
  await expect.poll(background).toBe(dark)
  await page.goto('/#/overview')
  await page.getByRole('link', { name: /^Open Pico Pro · Studio controls$/ }).click()
  const panel = page.locator('.front-panel')
  await expect(panel.getByLabel('Simulated Pico OLED display')).toBeVisible()
  const info = panel.locator('footer[aria-label="Machine information"]')
  await expect(info.getByText(/^Firmware /)).toBeVisible()
  for (const label of ['API v', 'UID ', 'Uptime '])
    await expect(info.getByText(label)).toBeVisible()
  await page.setViewportSize({ width: 360, height: 740 })
  await expect.poll(() => panel.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  await page.getByRole('button', { name: 'Manage machine' }).click()
  await page.getByRole('button', { name: 'Brewer preferences' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.goto('/#/settings')
  await appearance.selectOption('light')
  await page.emulateMedia({ colorScheme: 'dark' })
  await expect.poll(background).toBe(light)
  await appearance.selectOption('system')
  await expect.poll(background).toBe(dark)
})

test('the breadcrumb switches machines and returns to the fleet overview', async ({ page }) => {
  for (const width of [1280, 360]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/#/overview')
    const cards = page.locator('.machine-card')
    await expect(cards).toHaveCount(3)
    await expect(page.locator('.sidebar a[href^="#/devices/"]')).toHaveCount(0)
    const first = cards.first().getByRole('link', { name: /^Open .+ controls$/ })
    await first.click()
    const crumb = page.getByRole('navigation', { name: 'Breadcrumb' })
    const toggle = crumb.getByRole('button', { name: 'Switch machine' })
    await toggle.click()
    const machines = crumb.getByRole('list', { name: 'Machines' })
    await expect(machines.getByRole('link')).toHaveCount(3)
    await expect(machines.locator('a[aria-current="page"]')).toHaveCount(1)
    const next = machines.locator('a:not([aria-current])').first()
    const link = await next.getAttribute('href')
    await next.focus()
    await page.keyboard.press('Escape')
    await expect(toggle).toBeFocused()
    await expect(machines).toBeHidden()
    await toggle.click()
    await next.click()
    await expect(page).toHaveURL((url) => url.hash === link)
    await expect(machines).toBeHidden()
    await toggle.click()
    await expect(machines.locator('a[aria-current="page"]')).toHaveAttribute('href', link!)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
      true
    )
    await crumb.getByRole('link', { name: 'Brewery' }).click()
    await expect(cards).toHaveCount(3)
  }
})

test('journal readings and state lanes share one time cursor and note timestamp', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/#/history/demo-history-demo-pro')
  const chart = page.locator('.session-chart')
  const cursor = chart.getByRole('slider', { name: 'Inspect recorded samples' })
  await expect(cursor).toBeVisible()
  await expect(cursor.locator('svg')).toHaveCount(1)
  await expect(cursor.locator('[data-channel]')).toHaveCount(3)
  await expect(cursor.locator('[data-state]')).toHaveCount(6)
  const wort = chart.getByRole('button', { name: 'Wort', exact: true })
  await wort.click()
  await expect(wort).toHaveAttribute('aria-pressed', 'false')
  await expect(cursor.locator('[data-channel="wort_f"]')).toHaveCount(0)
  await wort.click()
  await cursor.press('Home')
  await expect(cursor).toHaveAttribute('aria-valuenow', '1')
  await cursor.press('ArrowRight')
  await expect(cursor).toHaveAttribute('aria-valuenow', '2')
  await expect(cursor).toHaveAttribute(
    'aria-valuetext',
    /0:15, Wort .+Thermoblock .+Target .+Phase .+Step .+Controller .+Flags Controller ready .+Machine busy .+Step filter inserted, Compartment .+Device state/
  )
  await page.getByRole('button', { name: 'Note at 0:15', exact: true }).click()
  await expect(page.getByLabel('Brew notes', { exact: true })).toHaveValue(/\[0:15\] $/)
  const bounds = await cursor.boundingBox()
  await cursor.click({ position: { x: bounds!.width * 0.7, y: 60 } })
  await expect(cursor).not.toHaveAttribute('aria-valuenow', '2')
  await chart.getByRole('button', { name: 'Unpin' }).click()
  await expect(page.getByRole('button', { name: /^Note at / })).toHaveCount(0)
  expect(await chart.evaluate((e) => e.scrollWidth <= e.clientWidth + 1)).toBe(true)
})

test('Brewery shows simultaneous sessions in full-width rows ahead of idle machines', async ({
  page,
}) => {
  await page.route('**/api/v1/events', (route) =>
    route.fulfill({ contentType: 'text/event-stream', body: '' })
  )
  await page.route('**/api/v1/devices', async (route) => {
    const response = await route.fetch()
    const devices = await response.json()
    const examples = [
      ['studio', 'Studio Porter', 'Mash'],
      ['pilot', 'Weekend Pale', 'Boil'],
      ['bench', '', ''],
    ]
    await route.fulfill({
      json: examples
        .map(([id, recipe, step], i) => ({
          ...devices[0],
          id,
          name: id,
          online: true,
          status: {
            ...devices[0].status,
            ready: true,
            busy: !!step,
            stopped: false,
            state: step ? 'brewing' : 'idle',
            recipe_name: recipe,
            step_name: step,
            current_step: i,
            step_count: 2,
            target_f: step ? 152 : 0,
          },
        }))
        .reverse(),
    })
  })
  await page.goto('/#/overview')
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 950 })
    const rows = page.locator('.brewery-row')
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0)).toContainText('Weekend Pale')
    await expect(rows.nth(0)).toContainText('Step 2 of 2 · Boil')
    await expect(rows.nth(1)).toContainText('Studio Porter')
    await expect(rows.nth(1)).toContainText('Step 1 of 2 · Mash')
    await expect(rows.last()).toContainText('Ready for the next session')
    const first = await rows.nth(0).boundingBox(),
      second = await rows.nth(1).boundingBox()
    expect(second!.y).toBeGreaterThanOrEqual(first!.y + first!.height)
    expect(first!.width).toBe(
      await page.locator('.brewery-list').evaluate((e) => e.getBoundingClientRect().width)
    )
  }
})

test('eight open Hub tabs can load and navigate over HTTP', async ({ context }) => {
  // Keep every tab open. This exceeds Chromium's six HTTP/1 connections per origin.
  const pages = []
  for (let i = 0; i < 8; i++) {
    const tab = await context.newPage()
    await tab.goto('/#/overview')
    await expect(tab.getByRole('heading', { name: 'Brewery', exact: true })).toBeVisible()
    pages.push(tab)
  }
  for (const tab of pages) {
    await tab.locator('.sidebar').getByRole('link', { name: 'Settings' }).click()
    await expect(tab.getByRole('heading', { name: 'Hub settings' })).toBeVisible()
  }
})

test('send a recipe and start it directly from the Hub', async ({ page, request }) => {
  const headers = { 'X-CSRF-Token': 'demo' }
  const name = 'Handoff brewer'
  const created = await request.post('/api/v1/devices', {
    headers,
    data: {
      name,
      adapter: 'simulator',
      model: 'pico-s',
      enabled: true,
    },
  })
  expect(created.ok()).toBe(true)
  const id = (await created.json()).id
  try {
    expect((await request.post(`/api/v1/devices/${id}/test`, { headers, data: {} })).ok()).toBe(
      true
    )
    await page.goto('/#/recipes')
    await page.getByRole('link', { name: /Weekend Pale/ }).click()
    await page.getByRole('button', { name: 'Send', exact: true }).click()
    await page.getByLabel('Target machine').selectOption(id)
    await page.getByRole('button', { name: 'Preview recipe' }).click()
    const receipt = page.waitForResponse((response) =>
      response.url().endsWith(`/devices/${id}/deploy`)
    )
    await expect(page.getByText('This will replace “Weekend Pale” on ' + name + '.')).toBeVisible()
    await page.getByRole('button', { name: 'Replace on machine' }).click()
    const sent = await (await receipt).json()
    expect(sent.started).toBe(false)
    await page.getByRole('button', { name: `Open ${name}` }).click()
    await expect(page.getByLabel('Recipe on this machine')).toHaveValue(sent.recipe.id)
    const commands: string[] = []
    page.on('request', (req) => {
      if (req.method() === 'POST' && req.url().endsWith(`/devices/${id}/commands`))
        commands.push(req.postDataJSON().command)
    })
    await page.getByRole('button', { name: 'Start recipe' }).click()
    await page.getByRole('button', { name: 'Start program' }).click()
    await expect(page.locator('.session-step')).toContainText('Mash')
    await page.goto('/#/overview')
    const row = page.locator('.brewery-row').filter({ has: page.getByRole('heading', { name }) })
    await expect(row).toContainText('Mash')
    await expect(row.getByRole('progressbar')).toBeVisible()
    expect(commands).toEqual(['brew'])
    await page.goto('/#/history/demo-history-demo-pro')
    await expect(page.locator('[data-state=current_step]')).toContainText('Mash out')
    const cursor = page.getByRole('slider', { name: 'Inspect recorded samples' })
    await cursor.focus()
    await cursor.press('End')
    await expect(cursor).toHaveAttribute('aria-valuetext', /Step 3 · Boil/)
  } finally {
    await request.delete(`/api/v1/devices/${id}`, { headers })
  }
})
