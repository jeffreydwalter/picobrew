'use strict';

const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {createRequire} = require('node:module');
const path = require('node:path');
const stockRequire = createRequire(path.resolve(__dirname, '../../../../reverse-engineered/tests/package.json'));
const {CPU, avrInstruction} = stockRequire('avr8js');
const [stock, candidate] = process.argv.slice(2).map(file => readFileSync(file));
const framebuffer = 0x266a;
const display = 0x1100;
const SPDR = 0x4e, SPSR = 0x4d;
const RETURN_PC = 0xffff;

function execute(image, entry, seed, sharedPort) {
    const words = new Uint16Array(0x10000).fill(0xffff);
    for (let i = 0; i < image.length; i += 2) words[i / 2] = image.readUInt16LE(i);
    const cpu = new CPU(words, 16384);
    const {data, dataView} = cpu;
    let waitReads = 0;
    const bus = [], spi = [], waits = [];
    for (let i = 2; i < 32; ++i) data[i] = (i * 31 + seed * 71) & 255;
    data[0x5f] = (seed * 37) & 0xff;
    dataView.setUint16(24, display, true);
    data.set([7, 8, 9, 10, 11], display + 24);
    dataView.setUint16(display + 33, 0x25, true);
    dataView.setUint16(display + 35, sharedPort ? 0x25 : 0x28, true);
    data[display + 39] = 1 << (seed % 8);
    data[display + 40] = 1 << ((seed + 4) % 8);
    for (let i = 0; i < 1024; ++i) data[framebuffer + i] = (i * (2 * seed + 1) + 193) & 255;
    for (const address of [...Array.from({length: 16}, (_, i) => 0x21 + i), 0x4c, SPSR, SPDR]) {
        data[address] = (73 * address + 23 * seed) & 0x7f;
        cpu.writeHooks[address] = value => {
            bus.push([address, value]);
            if (address === SPDR) {
                spi.push(value);
                waitReads = (seed + spi.length) % 4;
            }
            return false;
        };
        cpu.readHooks[address] = () => {
            let value = data[address];
            if (address === SPSR) value = (value & 0x7f) | (waitReads-- > 0 ? 0 : 0x80);
            bus.push([address + 0x100, value]);
            return value;
        };
    }
    cpu.pc = entry / 2;
    data[cpu.SP] = data[cpu.SP - 1] = 255;
    cpu.SP -= 2;
    const initialSP = cpu.SP;
    for (let i = 0; i < 500000; ++i) {
        if (cpu.pc === RETURN_PC) {
            assert.equal(cpu.SP, initialSP + 2);
            return {bus, spi, waits, registerIO: data.slice(0, 0x100), sram: data.slice(0x100, 0x3c00)};
        }
        if (cpu.pc === 0x19db0 / 2) {
            waits.push(dataView.getUint32(22, true));
            cpu.pc = 0x15e18 / 2; // replace the elapsed real-time wait with a stock RET
        }
        avrInstruction(cpu);
    }
    throw new Error('OLED routine timed out at 0x' + (2 * cpu.pc).toString(16));
}

let count = 0, bytes = 0;
for (const entry of [0x1623e, 0x1604e, 0x15e46]) {
    for (let seed = 0; seed < 8; ++seed) {
        for (const sharedPort of [false, true]) {
            const before = execute(stock, entry, seed, sharedPort);
            const after = execute(candidate, entry, seed, sharedPort);
            assert.deepEqual(after, before, 'OLED mismatch at 0x' + entry.toString(16) + ' for seed ' + seed);
            assert.equal(before.spi.length, entry === 0x1623e ? 23 : entry === 0x1604e ? 5 : 4102);
            if (entry === 0x1623e) assert.deepEqual(before.waits, [1, 10]);
            bytes += before.spi.length;
            ++count;
        }
    }
}
console.log(count + ' OLED comparisons passed (' + bytes + ' SPI transfers)');

function keyboard(image, entry, key, color, seed) {
    const words = new Uint16Array(65536).fill(0xffff);
    for (let i = 0; i < image.length; i += 2) words[i / 2] = image.readUInt16LE(i);
    const cpu = new CPU(words, 16384);
    const {data, dataView} = cpu;
    data.set(stock.subarray(0x1b24a), 0x100);
    if (seed) {
        // Seed both bytes to include multiplication wraparound and values
        // outside the physical screen; the original helpers accept uint8 keys.
        for (let i = 0x8a8; i < 0x8a8 + 256 * 6; ++i) data[i] = (i * 79 + seed) & 255;
    }
    for (let i = 2; i < 32; ++i) data[i] = (i * 23 + key) & 255;
    data[24] = key;
    data[22] = color;
    const args = [];
    cpu.pc = entry / 2;
    const stack = cpu.SP;
    data[stack] = data[stack - 1] = 255;
    cpu.SP -= 2;
    for (let i = 0; i < 500; ++i) {
        if (cpu.pc === RETURN_PC) {
            assert.equal(cpu.SP, stack);
            assert.equal(data[1], 0);
            return {args, saved: [...data.slice(2, 18), ...data.slice(28, 30)]};
        }
        if (cpu.pc === 0x1571c / 2 || cpu.pc === 0x1583a / 2) {
            args.push([cpu.pc, ...[24, 22, 20, 18, 16, 14].map(r => dataView.getUint16(r, true))]);
            cpu.pc = 0x15e18 / 2; // original primitive receives the full draw call
        }
        avrInstruction(cpu);
    }
    throw new Error('Key drawing did not return');
}
let keyCount = 0;
for (const entry of [0xa788, 0xa90a]) {
    for (let key = 0; key < 256; ++key) {
        for (const seed of [0, 171]) {
            const color = (key * 19 + seed) & 255;
            const before = keyboard(stock, entry, key, color, seed);
            const after = keyboard(candidate, entry, key, color, seed);
            assert.equal(before.args.length, 1);
            assert.deepEqual(after, before, 'Key geometry mismatch for ' + key);
            ++keyCount;
        }
    }
}
console.log(keyCount + ' key layout comparisons passed');
