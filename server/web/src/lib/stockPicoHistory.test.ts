import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, test } from 'vitest'
import type { BrewDetail, Sample } from '../types'
import { HistorySamples } from '../components/HistorySamples'
import { transitions } from './utils'
import { picoReportLabel } from './history'

test('stock Pico journal preserves reports and filters repeated periodic readings from the timeline', () => {
  const reading: Sample = {
    elapsed_ms: 0,
    source: 'stock-pico-report',
    wort_f: 152,
    thermoblock_f: 177,
    target_f: null,
    current_step: null,
    compartment: null,
    stock_pico: { kind: 'log', step: 'Mash in', controller_status: 0 },
  }
  const periodic = { ...reading, elapsed_ms: 9000 }
  const error: Sample = {
    ...reading,
    elapsed_ms: 12000,
    wort_f: null,
    thermoblock_f: null,
    stock_pico: { kind: 'error', error_code: 7 },
  }
  const state: Sample = { ...error, elapsed_ms: 14000, stock_pico: { kind: 'state', state: 2 } }
  const samples = [reading, periodic, error, state]
  expect(transitions(samples)).toEqual([reading, error, state])
  expect(samples.map((s) => picoReportLabel(s.stock_pico!))).toEqual([
    'Mash in',
    'Mash in',
    'Brewer error 7',
    'Brewer state 2',
  ])
  const brew = {
    model: 'pico-c',
    source: 'stock-pico-report',
    samples,
    sample_fields: ['elapsed_ms', 'wort_f', 'thermoblock_f'],
  } as BrewDetail
  const table = renderToStaticMarkup(createElement(HistorySamples, { brew, page: 0, unit: 'F' }))
  for (const text of ['<th>Brewer report</th>', 'Mash in', 'Brewer error 7', 'Brewer state 2'])
    expect(table).toContain(text)
  expect(table).not.toContain('<th>Target</th>')
})
