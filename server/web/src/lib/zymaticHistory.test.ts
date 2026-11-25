import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, test } from 'vitest'
import type { BrewDetail, Sample } from '../types'
import { HistorySamples } from '../components/HistorySamples'
import { transitions } from './utils'
import { sessionState } from './sessionState'
import { historyChannels, zymaticReportLabel, zymaticReportState } from './history'

test('Zymatic journal shows only recorded channels and labels keg, hops and the panel pause', () => {
  const empty: Sample = {
    zymatic_history: true,
    elapsed_ms: 0,
    wort_f: null,
    thermoblock_f: null,
    target_f: null,
    compartment: null,
    current_step: null,
    source: 'device',
  }
  const samples = [
    empty,
    { ...empty, elapsed_ms: 1000, compartment: 0, current_step: 0 },
    { ...empty, elapsed_ms: 61000, compartment: 2, current_step: 0 },
  ]
  const brew: BrewDetail = {
    id: 'a',
    device_id: 'a',
    device_name: 'Zymatic',
    model: 'zymatic',
    recipe_name: 'Pale ale',
    state: 'interrupted',
    command: 'brew',
    started_at: null,
    observed_at: '',
    duration_ms: 61000,
    device_history_id: 1,
    gap_count: 0,
    truncated: false,
    notes: '',
    rating: 0,
    sample_count: 3,
    source: 'device',
    samples,
    sample_fields: [
      'elapsed_ms',
      'wort_f',
      'thermoblock_f',
      'target_f',
      'compartment',
      'current_step',
    ],
  }
  const table = renderToStaticMarkup(createElement(HistorySamples, { brew, page: 0, unit: 'F' }))
  for (const title of ['Wort', 'Thermoblock', 'Target', 'Compartment', 'Step'])
    expect(table).toContain('<th>' + title + '</th>')
  for (const absent of ['Flags', 'Controller', 'State', 'Phase', 'Step time'])
    expect(table).not.toContain('<th>' + absent + '</th>')
  expect(table).toContain('Not recorded')
  expect(table).toContain('Keg')
  expect(table).toContain('Hop addition 1')
  expect(sessionState(brew, 'compartment', 6, 'device')).toBe('Operator pause')
  expect(transitions(samples)).toHaveLength(3)
})

test('stock Zymatic reports chart four temperatures and distinguish step state from fault flags', () => {
  const temp: Sample = {
    elapsed_ms: 1000,
    wort_f: 150,
    thermoblock_f: 177,
    target_f: null,
    current_step: null,
    compartment: null,
    source: 'zymatic-report',
    zymatic_history: true,
    readings: { sensor_board_f: 80, sensor_backup_heat_f: 174 },
    stock_zymatic: { kind: 'temperatures', error_flags: 64 },
  }
  const step: Sample = { ...temp, stock_zymatic: { kind: 'step', step_name: 'Mash in', state: 2 } }
  const reset: Sample = { ...temp, stock_zymatic: { kind: 'error', error_code: 303 } }
  const samples = [step, temp, { ...temp, elapsed_ms: 2000 }, reset]
  const schema = {
    model: 'zymatic',
    sample_fields: ['wort_f', 'thermoblock_f', 'sensor_board_f', 'sensor_backup_heat_f'],
    field_metadata: {
      sensor_board_f: { label: 'Control board', kind: 'temperature', unit: 'F' },
      sensor_backup_heat_f: { label: 'Backup heater', kind: 'temperature', unit: 'F' },
    },
    samples,
  }
  expect(historyChannels(schema).map((channel) => channel.field)).toEqual(schema.sample_fields)
  expect(transitions(samples)).toEqual([step, temp, reset])
  expect(zymaticReportLabel(step.stock_zymatic!)).toBe('Mash in')
  expect(zymaticReportState(step.stock_zymatic!)).toBe('Reported state 2')
  expect(zymaticReportState(temp.stock_zymatic!)).toBe('Sensor disconnected')
  expect(zymaticReportLabel(reset.stock_zymatic!)).toBe('Brownout reset')
})
