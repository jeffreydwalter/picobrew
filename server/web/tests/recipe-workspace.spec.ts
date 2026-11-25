import { test, expect } from '@playwright/test'

test.beforeEach(async ({ request }) => {
  expect((await (await request.get('/api/v1/auth/session')).json()).demo).toBe(true)
})

test('recipe browsing, filtered details and AI creation share one Recipes workspace', async ({
  page,
}) => {
  await page.goto('/#/recipes')
  const main = page.getByRole('navigation', { name: 'Main navigation' })
  const views = page.getByRole('navigation', { name: 'Recipe views' })
  await expect(main.getByRole('link', { name: /^Recipes/ })).toHaveAttribute('aria-current', 'page')
  await expect(main.getByRole('link', { name: /Community catalog|Brew assistant/ })).toHaveCount(0)
  await expect(views.getByRole('link')).toHaveCount(2)
  await expect(page.getByRole('button', { name: /community catalog/i })).toHaveCount(0)
  await views.getByRole('link', { name: 'Browse all' }).click()
  await page.getByPlaceholder('Search community recipes').fill('Pico Pale')
  await expect(page.getByRole('option', { name: 'Machine schedules' })).toHaveCount(0)
  await page.getByRole('button', { name: 'Sources & credits' }).click()
  await page.getByRole('button', { name: 'Browse source archive' }).click()
  await page.getByLabel('Archive record type').selectOption('native-program')
  await expect(page).toHaveURL(/q=Pico\+Pale/)
  await page.getByRole('button', { name: 'Pico Pale', exact: true }).first().click()
  await expect(
    page.getByRole('heading', { name: 'Pico Pale', exact: true, level: 1 })
  ).toBeVisible()
  await expect(page).toHaveURL(/catalog\/picopak-64\?.*q=Pico\+Pale/)
  await views.getByRole('link', { name: 'Browse all' }).click()
  await expect(page.getByPlaceholder('Search community recipes')).toHaveValue('Pico Pale')
  await expect(page.getByLabel('Archive record type')).toHaveValue('native-program')
  await expect(page.getByRole('link', { name: 'Create with AI', exact: true })).toHaveCount(0)
  await page.goto('/#/recipes/weekend-pale')
  await page.getByRole('button', { name: 'Ask the brew assistant', exact: true }).click()
  await expect(page.getByLabel('Existing recipe to build on (optional)')).toHaveValue(
    'Weekend Pale'
  )
  await expect(page.getByRole('heading', { name: 'Create a recipe with AI.' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Create with AI', exact: true })).toHaveCount(0)
  await expect(page.getByRole('tablist', { name: 'Assistant task' })).toHaveCount(0)
  await expect(main.getByRole('link', { name: /^Recipes/ })).toHaveAttribute('aria-current', 'page')
  await page.goto('/#/assistant?recipe=studio-porter')
  await expect(page).toHaveURL(/#\/recipes\/create-with-ai\?recipe=studio-porter$/)
  await expect(page.getByLabel('Existing recipe to build on (optional)')).toHaveValue(
    'Studio Porter'
  )
})

for (const width of [1440, 390]) {
  test(`readable typography and contained page layouts at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 960 })
    for (const route of [
      '/overview',
      '/recipes',
      '/recipes/studio-porter',
      '/catalog?kind=native-program&q=Pico%20Pale',
      '/recipes/create-with-ai',
      '/devices/demo-pro',
      '/history/demo-history-demo-pro?analysis=1',
      '/settings',
      '/api',
    ]) {
      await page.goto('/#' + route)
      await expect(page.locator('h1')).toBeVisible()
      await expect(page.getByRole('link', { name: 'Create with AI', exact: true })).toHaveCount(0)
      await expect(page.getByRole('button', { name: 'Create with AI', exact: true })).toHaveCount(0)
      const layout = await page.evaluate(() => ({
        width: innerWidth,
        scroll: document.documentElement.scrollWidth,
        base: parseFloat(getComputedStyle(document.body).fontSize),
        smallText: [
          ...document.querySelectorAll<HTMLElement | SVGElement>(
            'main p, main small, main label, main th, main td, main button, main input, main select, main svg text'
          ),
        ]
          .filter(
            (element) => element.checkVisibility() && element.getBoundingClientRect().height > 0
          )
          .map((element) => ({
            text: (element.textContent || element.getAttribute('aria-label') || element.tagName)
              .trim()
              .slice(0, 50),
            size: parseFloat(getComputedStyle(element).fontSize),
          }))
          .filter((element) => element.size > 0 && element.size < 14),
      }))
      expect(layout.scroll, `${route} horizontal overflow at ${width}px`).toBeLessThanOrEqual(
        layout.width + 1
      )
      expect(layout.base, `${route} body text`).toBeGreaterThanOrEqual(16)
      expect(layout.smallText, `${route} supporting text below 14px`).toEqual([])
      if (route === '/recipes' || route.startsWith('/history')) {
        await page.screenshot({
          path: test
            .info()
            .outputPath(`${route === '/recipes' ? 'recipes' : 'journal'}-${width}.png`),
          fullPage: true,
        })
      }
    }
  })
}

test('an empty library has one create action and the editor names each PicoPak flow path', async ({
  page,
}) => {
  await page.route('**/api/v1/recipes', (route) => route.fulfill({ json: [] }))
  await page.goto('/#/recipes')
  await expect(page.getByRole('button', { name: 'Create recipe', exact: true })).toHaveCount(0)
  const add = page.getByText('Add recipe', { exact: true })
  await expect(add).toHaveCount(1)
  await add.click()
  await page.getByRole('link', { name: 'Ask the brew assistant' }).click()
  await expect(page).toHaveURL(/#\/recipes\/create-with-ai$/)
  await expect(page.getByLabel('Existing recipe to build on (optional)')).toHaveValue('')
  await page.goBack()
  await add.click()
  await page.getByRole('link', { name: 'Create for a brewer', exact: true }).click()
  const brewerBatch = await page.getByLabel('Batch size (L)').getAttribute('placeholder')
  expect(brewerBatch).toBeTruthy()
  await page.getByRole('button', { name: 'Advanced editor' }).click()
  await expect(page.getByLabel('Batch size (L)')).toHaveValue(brewerBatch!)
  const valve = page.getByLabel('Flow path', { exact: true }).first()
  await expect(valve).toHaveValue('0')
  await expect(valve.getByRole('option', { name: 'Hop addition 1', exact: true })).toHaveAttribute(
    'value',
    '3'
  )
  await expect(valve).toHaveCSS('appearance', 'none')
  await expect(
    page.getByLabel('Step type', { exact: true }).first().locator('option:checked')
  ).toHaveText('Mash rest')
})

test('library actions follow selection and Escape uses the visible Back action', async ({
  page,
}) => {
  await page.goto('/#/recipes')
  const send = page.getByRole('button', { name: /Send selected/ })
  const shopping = page.getByRole('button', { name: /Shopping list/ })
  await expect(send).toHaveCount(0)
  await page.getByLabel('Select Weekend Pale').check()
  await expect(send).toBeVisible()
  await expect(shopping).toBeVisible()
  const add = page.getByText('Add recipe', { exact: true })
  await add.click()
  await expect(page.getByRole('link', { name: 'Ask the brew assistant' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(add).toBeFocused()
  await expect(page.getByRole('link', { name: 'Ask the brew assistant' })).toHaveCount(0)
  await page.goto('/#/recipes/weekend-pale')
  await page.getByRole('button', { name: 'Revision history' }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page).toHaveURL(/weekend-pale$/)
  await page.keyboard.press('Escape')
  await expect(page).toHaveURL(/#\/recipes$/)
  await page.goto('/#/recipes/new')
  await expect(page.getByRole('region', { name: 'Recipe wizard' })).toBeVisible()
  const header = await page.locator('.page-heading').boundingBox()
  const form = await page.getByRole('region', { name: 'Recipe wizard' }).boundingBox()
  expect(form?.width).toBe(header?.width)
  await page.getByLabel('Recipe name').fill('Unsaved test recipe')
  page.once('dialog', (dialog) => dialog.dismiss())
  await page.keyboard.press('Escape')
  await expect(page.getByLabel('Recipe name')).toHaveValue('Unsaved test recipe')
  page.once('dialog', (dialog) => dialog.accept())
  await page.keyboard.press('Escape')
  await expect(page).toHaveURL(/#\/recipes$/)
})

test('guide saves a Z recipe with correctly timed hop bins and a deployable preview', async ({
  page,
  request,
}) => {
  const name = 'Guided Z pale ale'
  await page.goto('/#/recipes/new')
  await page.getByLabel('Recipe name').fill(name)
  await page.getByLabel('Brewer', { exact: true }).selectOption('z')
  await page.getByLabel('Batch size (L)').fill('8')
  await expect(page.getByLabel('Flow path')).toHaveCount(0)
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByLabel('Grain', { exact: true }).fill('Pale malt')
  await page.getByLabel('Weight (kg)').fill('2')
  await page.getByRole('button', { name: 'Hop slot 4', exact: true }).click()
  await page.getByLabel('Hop variety').fill('Cascade')
  await page.getByLabel('Weight (g)').fill('20')
  await page.getByLabel('Minutes before boil ends').selectOption('15')
  await page.getByRole('button', { name: 'Add variety' }).click()
  await page.getByLabel('Hop variety').nth(1).fill('Amarillo')
  await page.getByLabel('Weight (g)').nth(1).fill('30')
  await expect(page.getByRole('button', { name: 'Continue' })).toBeDisabled()
  await page.getByLabel('Weight (g)').nth(1).fill('10')
  await page.getByRole('button', { name: 'Hop slot 1', exact: true }).click()
  await expect(
    page.getByLabel('Minutes before boil ends').locator('option[value="15"]')
  ).toHaveCount(0)
  await page.getByLabel('Minutes before boil ends').selectOption('60')
  await page.getByLabel('Hop variety').fill('Columbus')
  await page.getByLabel('Weight (g)').fill('8')
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  const schedule = page.getByRole('region', { name: 'Brew schedule' })
  const addition = schedule.getByRole('listitem').filter({ hasText: 'Slot 4 · Cascade + Amarillo' })
  await expect(addition).toContainText('15 min left')
  await expect(addition).toContainText('drain 10 min')
  await page.getByRole('button', { name: 'Save recipe' }).click()
  const entry = page.getByRole('link', { name: new RegExp(name) })
  await expect(entry).toBeVisible()
  const id = (await entry.getAttribute('href'))!.split('/').at(-1)!
  await entry.click()
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  await expect(page.getByLabel('Target machine')).toBeVisible()
  await expect(
    page.getByRole('dialog').getByLabel('Target batch size (L)', { exact: true })
  ).toHaveCount(0)
  try {
    const saved = await (await request.get('/api/v1/recipes/' + id)).json()
    expect(saved).toMatchObject({ name, source_model: 'z', batch_liters: 8, boil_minutes: 60 })
    const result = await request.post('/api/v1/recipes/' + id + '/adapt', {
      headers: { 'X-CSRF-Token': 'demo' },
      data: { profile_id: 'z', batch_liters: 8 },
    })
    expect(result.ok()).toBe(true)
    const plan = await result.json()
    expect(plan.can_deploy).toBe(true)
    expect(
      plan.machine_recipe.steps.map((s: { location: number; minutes: number }) => [
        s.location,
        s.minutes,
      ])
    ).toEqual([
      [1, 0],
      [2, 90],
      [1, 0], // Heat through the keg before wetting the first boil addition.
      [3, 45],
      [6, 15],
    ])
  } finally {
    await request.delete('/api/v1/recipes/' + id, { headers: { 'X-CSRF-Token': 'demo' } })
  }
})

test('mobile Pico wizard carries ingredients and generated steps into advanced editing', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/#/recipes/new')
  await page.getByLabel('Brewer', { exact: true }).selectOption('pico-s')
  await page.getByLabel('Recipe name').fill('Pico wheat')
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByLabel('Grain', { exact: true }).fill('Wheat malt')
  await page.getByLabel('Weight (kg)').fill('100')
  await expect(page.getByRole('alert')).toContainText('Reduce it to 2 kg')
  await expect(page.getByRole('button', { name: 'Continue' })).toBeDisabled()
  await page.getByLabel('Weight (kg)').fill('1.5')
  await page.getByRole('button', { name: 'Hop slot 3', exact: true }).click()
  await expect(page.getByLabel('Minutes before boil ends')).toBeVisible()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(page.getByText('Wheat malt · 1.5 kg')).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.getByRole('button', { name: 'Advanced editor' }).click()
  await expect(page.getByLabel('Recipe name', { exact: true })).toHaveValue('Pico wheat')
  await expect(page.getByLabel('Ingredient 1 name')).toHaveValue('Wheat malt')
  await expect(page.getByLabel('2. Step name', { exact: true })).toHaveValue('Dough in')
  await expect(page.getByLabel('Flow path', { exact: true }).nth(1)).toHaveValue('2')
})

test('four loaded slots support eight distinct visits through save and adaptation', async ({
  page,
  request,
}) => {
  await page.goto('/#/recipes/new')
  await page.getByLabel('Brewer', { exact: true }).selectOption('pico-s')
  const name = 'Eight visits pale ale'
  await page.getByLabel('Recipe name').fill(name)
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByLabel('Grain', { exact: true }).fill('Pale malt')
  await page.getByLabel('Weight (kg)').fill('1.2')
  const countdowns = [
    [60, 12],
    [52, 29],
    [45, 5],
    [38, 20],
  ]
  for (const [i, [first, again]] of countdowns.entries()) {
    await page.getByRole('button', { name: 'Hop slot ' + (i + 1), exact: true }).click()
    await page.getByLabel('Minutes before boil ends', { exact: true }).selectOption(String(first))
    await page.getByLabel('Hop variety').fill(['Cascade', 'Citra', 'Mosaic', 'Amarillo'][i])
    await page.getByLabel('Weight (g)').fill('8')
    await page.getByRole('button', { name: 'Add return visit' }).click()
    const repeat = page.getByLabel('Return 1 (minutes before boil ends)')
    await expect(repeat.locator('option[value="60"]')).toHaveCount(0)
    await repeat.selectOption(String(again))
  }
  await expect(page.getByText('Boil route', { exact: true }).locator('..')).toContainText(
    'Slot 4 (38 min) → Slot 2 (29 min) → Slot 4 (20 min) → Slot 1 (12 min) → Slot 3 (5 min)'
  )
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  const schedule = page.getByRole('region', { name: 'Brew schedule' })
  await expect(
    schedule.getByRole('listitem').filter({ hasText: 'Return to slot 2 · Citra' })
  ).toContainText('29 min left')
  await page.getByRole('button', { name: 'Save recipe' }).click()
  const id = (await page.getByRole('link', { name: new RegExp(name) }).getAttribute('href'))!
    .split('/')
    .at(-1)!
  const headers = { 'X-CSRF-Token': 'demo' }
  try {
    const result = await request.post('/api/v1/recipes/' + id + '/adapt', {
      headers,
      data: { profile_id: 'pico-s', batch_liters: 5 },
    })
    expect(result.ok()).toBe(true)
    const plan = await result.json()
    expect(plan.can_deploy).toBe(true)
    expect(plan.recipe.ingredients.filter((i: { kind: string }) => i.kind === 'hop')).toHaveLength(
      4
    )
    expect(
      plan.machine_recipe.steps
        .slice(-8)
        .map((s: { location: number; minutes: number }) => [s.location, s.minutes])
    ).toEqual([
      [3, 8],
      [4, 7],
      [5, 7],
      [6, 9],
      [4, 9],
      [6, 8],
      [3, 7],
      [5, 5],
    ])
  } finally {
    await request.delete('/api/v1/recipes/' + id, { headers })
  }
})

test('starting with an archived PicoPak preserves every original step through transfer preview', async ({
  page,
  request,
}) => {
  await page.goto('/#/recipes/new')
  await page.getByRole('button', { name: 'Start from an ingredient recipe' }).click()
  await expect(page).toHaveURL(/catalog\?kind=recipe/)
  const path = '/api/v1/catalog/picopak-1557'
  const original = await (await request.get(path + '/source?record=1557')).json()
  const headers = { 'X-CSRF-Token': 'demo' }
  const copy = await request.post(path + '/copy', { headers, data: {} })
  expect(copy.ok()).toBe(true)
  const { id } = await copy.json()
  try {
    for (const profile_id of ['pico-c', 'pico-s', 'pico-pro']) {
      const result = await request.post('/api/v1/recipes/' + id + '/adapt', {
        headers,
        data: { profile_id },
      })
      expect(result.ok()).toBe(true)
      const plan = await result.json()
      expect(plan.can_deploy).toBe(true)
      expect(plan.machine_recipe.steps).toEqual(
        original.Steps.map(
          (s: { Name: string; Location: number; Temp: number; Time: number; Drain: number }) => ({
            name: s.Name,
            location: s.Location,
            target_f: s.Temp,
            minutes: s.Time,
            drain_minutes: s.Drain,
          })
        )
      )
    }
  } finally {
    await request.delete('/api/v1/recipes/' + id, { headers })
  }
})

test('assistant filters both recipe sources and can change a catalog selection to a library recipe', async ({
  page,
}) => {
  await page.goto('/#/recipes/create-with-ai')
  const search = page.getByRole('searchbox', { name: 'Existing recipe to build on (optional)' })
  await search.fill('pale')
  const results = page.locator('[aria-label="Recipe search results"]')
  await expect(results.getByRole('button', { name: /Weekend Pale/ }).first()).toBeVisible()
  await search.fill('Dead Guy Clone')
  await results
    .getByRole('button', { name: /Dead Guy Clone/i })
    .first()
    .click()
  await expect(search).toHaveValue(/Dead Guy Clone/i)
  await expect(page.getByText(/^Community catalog ·/)).toBeVisible()
  await page.getByRole('button', { name: 'Clear selection' }).click()
  await search.fill('studio porter')
  await results
    .getByRole('button', { name: /Studio Porter/ })
    .first()
    .click()
  await expect(page.getByText(/^My library · Studio Porter/)).toBeVisible()
})

test('preview and publish a Zymatic recipe from the real transfer dialog', async ({ page }) => {
  await page.goto('/#/recipes/new')
  await page.getByLabel('Recipe name').fill('Stock sync test ale')
  await page.getByLabel('Brewer', { exact: true }).selectOption('zymatic')
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByLabel('Grain', { exact: true }).fill('Pale malt')
  await page.getByLabel('Weight (kg)').fill('1.5')
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('button', { name: /save recipe/i }).click()
  await page.getByRole('link', { name: /^Stock sync test ale/ }).click()
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  await page.getByLabel('Target machine').selectOption('stock-zymatic-menu')
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Zymatic serial number').fill('ABC123XYZ999')
  await dialog.getByRole('button', { name: 'Preview stock menu' }).click()
  await expect(dialog.getByRole('cell', { name: 'Keg circulation' }).first()).toBeVisible()
  await dialog.getByRole('button', { name: 'Publish to brewer menu' }).click()
  await expect(dialog.getByText(/Published for ABC123XYZ999/)).toBeVisible()
})

test('an archived PicoPak shows the published ingredients and shares an editable style list', async ({
  page,
  request,
}) => {
  await page.goto('/#/catalog/picopak-46')
  const ingredients = page.getByRole('region', { name: 'Published ingredients' })
  await expect(ingredients).toContainText('Bohemian Pilsner')
  await expect(ingredients).toContainText('Crystal')
  await page.getByRole('button', { name: 'Copy to my library' }).click()
  const style = page.getByRole('combobox', { name: 'Style', exact: true })
  await expect(style).toHaveValue('Helles')
  const id = page.url().split('/recipes/').at(-1)!
  const exportProgram = page.getByRole('button', { name: 'Export program JSON' })
  await expect(exportProgram).toBeEnabled()
  try {
    await style.fill('light lager')
    await expect(
      page.getByRole('option', { name: 'American Light Lager (BJCP 2015 1A)', exact: true })
    ).toBeVisible()
    await style.press('ArrowDown')
    await style.press('Enter')
    await expect(style).toHaveValue(/lager/i)
    await style.fill('My cellar lager')
    await page.getByRole('option', { name: 'Use “My cellar lager”' }).click()
    await expect(exportProgram).toBeDisabled()
    await page.getByRole('button', { name: 'Save program' }).click()
    await expect(page.getByText('PicoPak saved to your library.')).toBeVisible()
    await expect(exportProgram).toBeEnabled()
    await page.goto('/#/recipes/new')
    const picker = page.getByRole('combobox', { name: 'Beer style' })
    await picker.fill('cellar')
    await page.getByRole('option', { name: 'My cellar lager', exact: true }).click()
    await expect(picker).toHaveValue('My cellar lager')
  } finally {
    await request.delete('/api/v1/recipes/' + id, { headers: { 'X-CSRF-Token': 'demo' } })
  }
})

test('brew-ready catalog carries the author’s dry hop and original program to a stock preview', async ({
  page,
}) => {
  await page.goto('/#/catalog?q=UB08')
  await page.getByRole('button', { name: 'UB08 Summer IPA' }).click()
  await expect(page.getByText('Dry hop with 25 g Citra for 7 days.', { exact: true })).toBeVisible()
  const ingredients = page.getByRole('table', { name: 'Scaled ingredient bill' })
  await expect(ingredients.getByRole('row').filter({ hasText: 'Dry hop · 7 days' })).toContainText(
    'Citra'
  )
  await page.getByRole('button', { name: 'Save Zymatic program' }).click()
  await expect(page.getByLabel('Program notes')).toContainText('Dry hop with 25 g Citra')
  await page.getByRole('button', { name: 'Send' }).click()
  const dialog = page.getByRole('dialog', { name: 'Send to stock Zymatic' })
  await dialog.getByLabel('Zymatic serial number').fill('CATALOGPREVIEW')
  await dialog.getByRole('button', { name: 'Preview stock menu' }).click()
  await expect(dialog.getByRole('cell', { name: 'Hop addition 1' }).first()).toBeVisible()
})

test('catalog and library show five automatic versions from one measured recipe', async ({
  page,
  request,
}) => {
  await page.goto('/#/catalog?kind=brew-ready&q=UB08&model=pico-s')
  await expect(page.getByRole('heading', { name: 'Community recipes' })).toBeVisible()
  await expect(page.getByLabel('Catalog type')).toHaveCount(0)
  await expect(page.getByRole('option', { name: 'Verified for stock Zymatic' })).toHaveCount(0)
  await page.getByRole('button', { name: 'UB08 Summer IPA', exact: true }).click()
  const versions = page.getByRole('region', { name: 'Machine versions' })
  const target = versions.getByLabel('View brewer version')
  await expect(target).toHaveValue('pico-s')
  await expect(target.locator('option')).toHaveCount(5)
  await expect(versions.getByRole('table', { name: 'Scaled ingredient bill' })).toContainText(
    'Citra'
  )
  for (const id of ['pico-s', 'pico-c', 'pico-pro', 'z', 'zymatic']) {
    await target.selectOption(id)
    await expect(versions.getByRole('columnheader', { name: 'Flow path' })).toBeVisible()
  }
  await page.getByRole('button', { name: 'Copy ingredient recipe' }).click()
  const view = page.getByRole('combobox', { name: 'Recipe view', exact: true })
  await expect(view.locator('option')).toHaveCount(6)
  const sourceVolume = await page.getByLabel('Batch size (L)').inputValue()
  await view.selectOption('pico-c')
  await expect(page.getByRole('table', { name: 'Scaled ingredient bill' })).toContainText(
    'Hop slot'
  )
  await view.selectOption('pico-s')
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  const deviceID = await page.getByLabel('Target machine').inputValue()
  expect(deviceID).toBeTruthy()
  const device = await request.get('/api/v1/devices/' + encodeURIComponent(deviceID))
  expect(await device.json()).toMatchObject({ model: 'pico-s' })
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await view.selectOption('')
  await expect(page.getByLabel('Batch size (L)')).toHaveValue(sourceVolume)
})

test('a recipe plan can be recorded separately for each batch after brew day', async ({
  page,
  request,
}) => {
  const headers = { 'X-CSRF-Token': 'demo' }
  const source = await (await request.get('/api/v1/recipes/weekend-pale')).json()
  const response = await request.post('/api/v1/recipes', {
    headers,
    data: {
      ...source,
      id: '',
      revision: 0,
      name: 'Journal test pale',
      fermentation: [{ name: 'Primary fermentation', target_c: 18, days: 10 }],
      carbonation: { method: 'natural', volumes: 2.4 },
    },
  })
  expect(response.ok()).toBe(true)
  const r = await response.json()
  try {
    await page.goto('/#/history/demo-history-demo-s')
    await page.getByLabel('Use a recipe plan').selectOption(r.id)
    await page.getByRole('button', { name: 'Use plan', exact: true }).click()
    await page.getByRole('button', { name: 'Start Primary fermentation' }).click()
    await page.getByRole('button', { name: 'Complete Primary fermentation' }).click()
    await page.reload()
    await expect(page.getByRole('row').filter({ hasText: 'Primary fermentation' })).toContainText(
      '10 days'
    )
    await expect(
      page.getByRole('button', { name: 'Start Bottle or keg conditioning' })
    ).toBeVisible()
    const brew = await (
      await request.get('/api/v1/brews/demo-history-demo-s/export?format=json')
    ).json()
    expect(brew.conditioning.stages[0].completed_at).toBeTruthy()
    expect(brew.conditioning.stages[1].started_at).toBeUndefined()
    const stale = await request.post('/api/v1/brews/demo-history-demo-s/conditioning', {
      headers,
      data: { revision: 0, action: 'add', name: 'Duplicate' },
    })
    expect(stale.status()).toBe(409)
    expect(
      (await (await request.get('/api/v1/brews/demo-history-demo-z')).json()).conditioning
    ).toBeUndefined()
  } finally {
    await request.delete('/api/v1/recipes/' + r.id, { headers })
  }
})

test('a shared recipe defaults to 2.5 US gallons and preserves a custom yield across brewer views', async ({
  page,
}) => {
  await page.goto('/#/recipes')
  await page.getByText('Add recipe', { exact: true }).click()
  await page.getByRole('link', { name: 'Create by batch size' }).click()
  await page.getByLabel('Recipe measurements').selectOption('us')
  await expect(page.getByLabel('Batch size (US gal)')).toHaveValue('2.5')
  await page.getByLabel('Batch size (US gal)').fill('5')
  await expect(page.getByLabel('2. Step name')).toHaveValue('Boil')
  await page.getByLabel('Recipe name', { exact: true }).fill('Five gallon base')
  await page.getByRole('button', { name: 'Add grain' }).click()
  await page.getByLabel('Ingredient 1 name').fill('Pale malt')
  await page.getByLabel('Amount (lb)', { exact: true }).fill('10')
  await page.getByRole('button', { name: 'Save recipe', exact: true }).click()
  const view = page.getByRole('combobox', { name: 'Recipe view', exact: true })
  await expect(view).toBeVisible()
  await view.selectOption('pico-s')
  await expect(page.getByRole('table', { name: 'Scaled ingredient bill' })).toContainText('4.41 lb')
  await expect(page.getByRole('columnheader', { name: 'Flow path' })).toBeVisible()
  await view.selectOption('')
  await expect(page.getByLabel('Amount (lb)', { exact: true })).toHaveValue('10')
  await expect(page.getByLabel('Batch size (US gal)')).toHaveValue('5')
})

test('model-specific authoring preserves Z Program Maker routes, operator pauses, and seconds', async ({
  page,
  request,
}) => {
  await page.goto('/#/recipes')
  await page.getByText('Add recipe', { exact: true }).click()
  await page.getByRole('link', { name: 'Create a machine program' }).click()
  await page.getByLabel('Brewer', { exact: true }).selectOption('pico-s')
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(page.getByLabel('Program step 1 name')).toBeVisible()
  await expect(page.getByLabel('Use PicoPak adjunct compartments')).toHaveCount(0)
  await page.goto('/#/recipes/new')
  await page.getByLabel('Brewer', { exact: true }).selectOption('z')
  page.once('dialog', async (dialog) => dialog.accept())
  await page.getByRole('button', { name: 'Open Z Program Maker' }).click()
  await page.getByRole('button', { name: 'Load sample' }).click()
  await page.getByLabel('Program name', { exact: true }).fill('Route and pause')
  await expect(page.getByLabel('Temperature units')).toHaveValue('C')
  await page.getByLabel('Temperature units').selectOption('F')
  await expect(page.getByLabel('Target (°F)', { exact: true }).first()).toHaveValue('152.78')
  await page.getByLabel('Temperature units').selectOption('C')
  await expect(page.getByLabel('Target (°C)', { exact: true }).first()).toHaveValue('67.1')
  await page.getByLabel('Use PicoPak adjunct compartments').check()
  await page.getByLabel('Hold (min:sec)').last().fill('4:17')
  await page.getByRole('button', { name: 'Add step' }).click()
  await page.getByLabel('Step type', { exact: true }).last().selectOption('2')
  await page.getByLabel('Step name', { exact: true }).last().fill('Inspect filter')
  await expect(page.getByText('The Z waits here until the operator continues.')).toBeVisible()
  await page.getByRole('button', { name: 'Save program', exact: true }).click()
  await expect(page).toHaveURL(/#\/recipes\/(?!new)[^/?]+$/)
  const id = page.url().split('/').pop()
  const exported = await request.get('/api/v1/recipes/' + id + '/export?format=z-program')
  expect(exported.ok(), await exported.text()).toBe(true)
  const program = await exported.json()
  expect(program.Name).toBe('Route and pause')
  expect(program.IsMetric).toBe(true)
  expect(program.UsePicoPakMode).toBe(true)
  expect(program.Steps[6]).toMatchObject({ Location: 6, Time: 257, Drain: 300 })
  expect(program.Steps[7]).toEqual({ Name: 'Inspect filter', StepType: 2 })
  await page.reload()
  await expect(page.getByLabel('Hold (min:sec)').last()).toHaveValue('4:17')
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  const target = page.getByLabel('Transfer to machine')
  await expect(target.getByRole('option', { name: /Stock Zymatic|Pico S|Pico Pro/ })).toHaveCount(0)
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await page.getByRole('button', { name: 'Import program', exact: true }).click()
  await page
    .getByLabel('Or paste the recipe')
    .fill(JSON.stringify({ ...program, Name: 'Imported exact Z', IsMetric: false }))
  await page.getByRole('button', { name: 'Import recipes', exact: true }).click()
  await expect(page.getByLabel('Program name', { exact: true })).toHaveValue('Imported exact Z')
  await expect(page.getByLabel('Temperature units')).toHaveValue('F')
  await expect(page.getByLabel('Use PicoPak adjunct compartments')).toBeChecked()
  await expect(page.getByLabel('Flow path').last()).toHaveValue('6')
  await page.getByRole('button', { name: 'Import program', exact: true }).click()
  const { IsMetric: _, ...withoutUnit } = program
  withoutUnit.Name = 'No unit specified'
  withoutUnit.Steps[0].Temp = null
  delete withoutUnit.Steps.at(-1).Time
  await page.getByLabel('Or paste the recipe').fill(JSON.stringify(withoutUnit))
  await page.getByRole('button', { name: 'Import recipes', exact: true }).click()
  await expect(page.getByLabel('Program name', { exact: true })).toHaveValue(withoutUnit.Name)
  await expect(page.getByLabel('Temperature units')).toHaveValue('')
  await page.getByLabel('Temperature units').selectOption('F')
  await page.getByLabel('Temperature units').selectOption('')
  await page.getByRole('button', { name: 'Save program', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Export Z program' })).toBeEnabled()
  const restored = await request.get(
    '/api/v1/recipes/' + page.url().split('/').pop() + '/export?format=z-program'
  )
  expect(await restored.json()).toEqual(withoutUnit)
})

test('Z Program Maker can author and reopen a 20-step program', async ({ page, request }) => {
  const imported = await request.post('/api/v1/recipes/import', {
    headers: { 'X-CSRF-Token': 'demo' },
    data: {
      format: 'json',
      data: JSON.stringify({
        Name: 'Extended schedule',
        IsBeer: true,
        UsePicoPakMode: false,
        Steps: Array.from({ length: 19 }, (_, i) => ({ Name: 'Check ' + (i + 1), StepType: 2 })),
      }),
    },
  })
  expect(imported.ok()).toBe(true)
  const [recipe] = await imported.json()
  await page.goto('/#/recipes/' + recipe.id)
  const add = page.getByRole('button', { name: 'Add step', exact: true })
  await add.click()
  await expect(add).toBeDisabled()
  await page.getByLabel('Step name', { exact: true }).last().fill('Finish')
  await page.getByRole('button', { name: 'Save program', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Send', exact: true })).toBeEnabled()
  await page.reload()
  await expect(page.getByLabel('Step name', { exact: true })).toHaveCount(20)
  await expect(page.getByLabel('Step name', { exact: true }).last()).toHaveValue('Finish')
})

test('stock Zymatic authoring enforces the brewer temperature limit and creates an operator pause', async ({
  page,
}) => {
  await page.goto('/#/recipes/new?mode=program&model=zymatic')
  await page.getByLabel('Program name', { exact: true }).fill('Check the filter')
  const temp = page.getByLabel('Step 1 target (°F)')
  await temp.fill('208')
  await page.getByRole('button', { name: 'Save program' }).click()
  await expect(page.getByText(/exceeds the Zymatic limit of 207/)).toBeVisible()
  await temp.fill('152')
  await page.getByRole('button', { name: 'Add program step' }).click()
  await page.getByLabel('Program step 2 name').fill('Inspect filter')
  await page.getByLabel('Step 2 flow path').selectOption('6')
  for (const field of ['target (°F)', 'hold (min)', 'drain (min)']) {
    const input = page.getByLabel('Step 2 ' + field)
    await expect(input).toHaveValue('0')
    await expect(input).toBeDisabled()
  }
  await page.getByRole('button', { name: 'Save program' }).click()
  await expect(page.getByRole('alert')).toContainText('Add a step after the pause or remove it.')
  await page.getByRole('button', { name: 'Add program step' }).click()
  await page.getByLabel('Program step 3 name').fill('Chill')
  await page.getByLabel('Step 3 flow path').selectOption('0')
  await page.getByLabel('Step 3 target (°F)').fill('65')
  await page.getByRole('button', { name: 'Save program' }).click()
  await expect(page).toHaveURL(/#\/recipes\/(?!new)[^/?]+$/)
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Send to stock Zymatic' })
  await dialog.getByLabel('Zymatic serial number').fill('PAUSETST')
  await dialog.getByRole('button', { name: 'Preview stock menu' }).click()
  const pause = dialog.getByRole('row').filter({ hasText: 'Inspect filter' })
  await expect(pause.getByRole('cell', { name: 'Pause', exact: true })).toBeVisible()
  await expect(pause).not.toContainText('Advance immediately')
  await expect(dialog.getByText(/72 min \+ heating \+ pauses/)).toBeVisible()
})
