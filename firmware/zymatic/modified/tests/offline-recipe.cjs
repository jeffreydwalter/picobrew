'use strict';

const assert = require('node:assert/strict');

module.exports = function testOffline({cpu, words, run, execute, word, image}) {
  const read = addr => cpu.dataView.getUint16(addr, true);
  function string(addr) {
    const end = cpu.data.indexOf(0, addr);
    assert(end >= addr && end - addr < 512);
    return Buffer.from(cpu.data.subarray(addr, end)).toString('ascii');
  }
  function target(pc, jump = false) {
    assert.equal(words[pc/2], jump ? 0x940c : 0x940e);
    const address = words[pc/2+1] * 2;
    assert(address >= 0x1bc00 && address < image.length);
    return address;
  }
  const sync = target(0xdb88), begin = target(0xf732), start = target(0xd646);
  const recover = target(0x100ca), bootMenu = target(0x110ba), branch = target(0x110c2, true);
  assert.equal(sync, target(0xdb94));

  const config = {155: 1, 156: 14, 1: 0, 121: 0, 67: 65, 88: 66};
  const eeprom = new Uint8Array(4096);
  const recipe = 0x16be, ids = 0x1d76, count = 0x1ec5, key = 0x23f6;
  const expected = process.argv[4] ? JSON.parse(require('node:fs').readFileSync(process.argv[4], 'utf8')) : null;
  const name = expected?.name || 'American Brown Ale';
  const id = expected?.id || 'cc7f7047d96443d04dfcda7e08d50b69';
  const steps = expected ? expected.steps.map(s => [s.name,s.target_f,s.minutes,s.location,s.drain_minutes]) : [
    ['Heat Water', 152, 0, 0, 0], ['Single Infusion', 152, 90, 1, 8], ['Heat to Boil', 207, 0, 0, 0],
    ['Hops 1', 207, 45, 2, 0], ['Hops 2', 207, 5, 3, 0], ['Hops 3', 207, 8, 4, 0],
    ['Hops 4', 207, 2, 5, 10], ['Connect Chiller', 0, 0, 6, 0], ['Chill', 65, 10, 0, 10],
  ];

  // Enter via the stock startup call and exercise the three original exits.
  for (const corrupted of [null, 1, 121, 155, 156, 67, 88]) {
    for (const choice of [0, 1]) for (const wifi of [0, 1]) {
      Object.assign(eeprom, config);
      if (corrupted !== null) eeprom[corrupted] = 0xff;
      let shown = 0, configured = 0;
      const result = run(bootMenu, {
        0x4862: () => { cpu.data[24] = eeprom[read(24)]; },
        0x81f0: () => {
          assert.equal(string(read(24)), 'Network unavailable');
          assert.equal(string(0x2010), 'Set up network');
          assert.equal(string(0x2010+21), 'Continue offline');
          assert.equal(cpu.data[22], 2); assert.equal(cpu.data[20], 0);
          shown++; cpu.data[24] = choice;
        },
        0xb232: () => { configured++; cpu.data[24] = wifi; },
      });
      const local = corrupted === null && choice === 1;
      assert.equal(shown, corrupted === null ? 1 : 0);
      assert.equal(configured, Number(!local));
      cpu.data[0x230c] = result;
      assert.equal(result, local ? 0 : wifi);
      assert.equal(execute(branch, [0x110ca, 0x110e2, 0x11054]), local ? 0x110e2 : wifi ? 0x110ca : 0x11054);
    }
  }

  // Cold power-on with no Hub or associated radio, previously commissioned.
  // Start at OEM setup and retain the identity/EEPROM/branch logic, including
  // the unmodified interrupted-session decision. Only board I/O, long display
  // operations, and the separate application-integrity check are substituted.
  Object.assign(eeprom, config);
  cpu.data.fill(0, 0x0a80, 0x2c8a);
  for (const base of [67, 88]) for (let i = 1; i < 10; i++) eeprom[base+i] = 0;
  const bootCalls = {};
  for (const pc of [0xb97c, 0x1604e, 0x3c0a, 0xa3f6, 0x4408, 0x1131c, 0x76ee,
    0x7ba2, 0x445a, 0x7758, 0x27a8, 0x7a74]) bootCalls[pc] = () => {};
  for (const pc of [0x54b2, 0x10252, 0x48c2]) bootCalls[pc] = () => { cpu.data[24] = 0; };
  bootCalls[0x4862] = () => { cpu.data[24] = eeprom[read(24)]; };
  bootCalls[0x81f0] = () => { assert.equal(string(read(24)), 'Network unavailable'); cpu.data[24] = 1; };
  run(0x10fac, bootCalls);
  assert.equal(cpu.data[0x165d], 65); assert.equal(cpu.data[0x1672], 66);
  assert.equal(cpu.data[0x230b], 0); assert.equal(cpu.data[0x230c], 0);

  // Continue into the physical top-level menu. Stop at the first instruction
  // of the stock brew runner, before any motor or output command. The restore
  // guard is enabled by the original db5a menu code.
  const originalSP = cpu.SP;
  const screens = [];
  const coldCalls = {
    0xa386: () => {}, 0x44fa: () => {}, 0x1604e: () => {}, 0x8734: () => {},
    0x81f0: () => {
      screens.push(Array.from({length: cpu.data[22]}, (_, row) => string(0x2010 + row * 21)));
      cpu.data[24] = 0;  // Select Brew Recipe, then the first available recipe.
    },
  };
  execute(0x10660, [0xf6fe], coldCalls);
  assert.equal(screens.length, 2);
  assert.deepEqual(screens[0], ['Brew Recipe', 'Clean/Rinse', 'Machine Setup', 'Help', 'About Zymatic', 'Delayed Start']);
  assert(screens[1][0].includes(name.slice(0, 18)));
  assert.equal(cpu.data[0x089d], 1);
  cpu.SP = originalSP;

  // A real online catalog, cleaning request or unexpected count is never
  // replaced. 12 is the stock parser's empty-menu sentinel.
  function load(mode, incoming) {
    cpu.data[24] = mode;
    cpu.data[0x230b] = 1;
    const wantedRamp = cpu.data[0x5b];
    run(sync, {
      0xdade: () => { assert.equal(cpu.data[24], mode); cpu.data[count] = incoming; },
      0x8734: () => {}, // OEM parser's display progress updates.
    });
    assert.equal(cpu.data[0x5b], wantedRamp);
  }
  for (const n of [1, 2, 10, 11, 13, 255]) {
    load(0, n);
    assert.equal(cpu.data[count], n);
  }
  load(1, 0);
  assert.equal(cpu.data[count], 0);
  for (const empty of [12, 0, 0]) {
    load(0, empty);
    assert.equal(cpu.data[count], 1);
    assert.equal(string(recipe), name);
    assert.equal(string(ids), id);
    assert.equal(cpu.data[recipe + 21], steps.length);
    steps.forEach((step, i) => {
      const addr = recipe + 22 + 6 * i;
      const ptr = read(addr);
      assert(ptr >= 0x0a81 && ptr < 0x1639);
      assert.deepEqual([string(ptr), ...cpu.data.subarray(addr+2, addr+6)], step);
    });
  }

  // Normal selection goes through the actual recipe-name formatter, physical
  // menu, then at most one brew launch. Back and an out-of-range selection
  // return without issuing a start. No physical output routine is emulated.
  for (const choice of [0, 2, 3]) {
    let presses = 0, brews = 0;
    cpu.data[24] = 0; cpu.data[22] = 0; cpu.data[20] = 0;
    run(0xfd4e, {
      0x81f0: () => {
        assert.equal(cpu.data[22], 3);
        assert(string(0x2010).includes(name.slice(0, 18)));
        assert.equal(brews, 0);
        presses++; cpu.data[24] = choice;
      },
      0xf6fe: () => { assert.equal(presses, 1); assert.equal(cpu.data[24], 0); brews++; },
      0x1604e: () => {}, 0x4caa: () => {}, 0xbf84: () => {}, 0x4c52: () => {},
    });
    assert.equal(brews, Number(choice === 0));
  }

  // The key is invalidated before the running-brew flag. If the power goes
  // away between those writes, a previous remote ID is never resumable.
  const order = [];
  const storage = {0x495c: () => {
    const addr = read(24), value = cpu.data[22];
    order.push([addr, value]); eeprom[addr] = value;
  }};
  cpu.data[24] = 1;
  run(begin, storage);
  assert.deepEqual(order, [[122, 33], [1, 1]]);
  cpu.data.fill(0x61, key, key+33);
  word(24, 0x1672); word(22, ids); cpu.data[20] = 0;
  assert.equal(run(start), 1); assert.equal(string(key), '!');
  assert(cpu.data.subarray(key+1,key+33).every(b => b === 0));
  word(24, key); assert.equal(run(recover), 0);
  cpu.data[key] = 0x61;
  word(24, key); assert.equal(run(recover, {0xd886: () => { cpu.data[24] = 1; }}), 1);

  // A future remote session with a different ID retains its original result,
  // including failure/cancel. Resuming a remote brew also keeps that result.
  cpu.data[ids] ^= 1;
  cpu.data[24] = 1; order.length = 0;
  run(begin, storage);
  assert.deepEqual(order, [[1, 1]]);
  word(24, 0x1672); word(22, ids); cpu.data[20] = 0;
  let remote = 0;
  assert.equal(run(start, {0xd512: () => { remote++; cpu.data[24] = 0; }}), 0);
  assert.equal(remote, 1);
  console.log('Offline recipe passed OEM parser, chooser, session, and commissioned cold-boot checks.');
};
