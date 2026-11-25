import { test, expect } from 'vitest'
import { plannedHold, programTiming, routeLabel } from './programs'

test('PicoPak steps use brewing names and Z archives retain their own routing', () => {
  expect([1, 2, 3, 4, 5, 6].map((n) => routeLabel(n, 'pico-s'))).toEqual([
    'Heat water',
    'Mash',
    'Hop addition 1',
    'Hop addition 2',
    'Hop addition 3',
    'Hop addition 4',
  ])
  expect(routeLabel(0, 'pico')).toBe('Unassigned')
  expect(routeLabel(0, 'z', true)).toBe('Keg circulation')
  expect(routeLabel(5, 'z', true)).toBe('Hop addition 4')
  expect(routeLabel(6, 'zymatic', true)).toBe('Pause')
  expect(routeLabel(1, 'z')).toBe('Keg circulation')
  expect(routeLabel(6, 'z')).toBe('Hop addition 4')
})

test('Z kit times follow the original descending strength range and skip operator pauses', () => {
  const step = { name: 'Mash', location: 2, target_f: 152, minutes: 20, drain_minutes: 2 }
  const program = {
    id: 'kit',
    name: 'Kit',
    steps: [step, { ...step, name: 'Operator pause' }],
    z_pak: {
      recipe_id: 1,
      beer_id: 47,
      abv: 5,
      ibu: 6,
      selected_abv: 8,
      step_options: [{ action: 0, min_time: 25, max_time: 15 }, { action: 1 }],
    },
  }
  expect(plannedHold(program, 0)).toBe(17)
  expect(programTiming(program)).toBe('19 min + heating + pauses')
  program.z_pak.selected_abv = 2
  expect(plannedHold(program, 0)).toBe(23)
  program.z_pak.selected_abv = 5
  expect(plannedHold(program, 0)).toBe(20)
})
