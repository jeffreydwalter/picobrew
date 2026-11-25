import { test, expect } from 'vitest'
import { routeLabel } from './programs'

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
