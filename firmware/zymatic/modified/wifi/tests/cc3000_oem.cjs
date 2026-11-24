'use strict';

// Compare the C listener's actual HCI traffic with the unmodified 1.1.14
// socket builders and event decoder. The SPI peripheral is a call boundary.
const assert = require('node:assert/strict');
const { createHash } = require('node:crypto');
const { execFileSync } = require('node:child_process');
const { readFileSync } = require('node:fs');
const { createRequire } = require('node:module');
const path = require('node:path');
const stockRequire = createRequire(path.resolve(__dirname, '../../../reverse-engineered/tests/package.json'));
const { CPU, avrInstruction } = stockRequire('avr8js');
const [imageFile, testExe, avrBin] = process.argv.slice(2);
if (!imageFile || !testExe) throw Error('Provide the decoded original 1.1.14 BIN and native CC3000 test executable');
const image = readFileSync(imageFile);
assert.equal(createHash('sha256').update(image).digest('hex'),
             'd6bff3e9c49c092a9ed825b91790c0adbc46be7112f3d79f26d9a30d28e0a4cd');
const words = new Uint16Array(65536);
words.fill(0xffff);
for (let i = 0; i < image.length; i += 2) words[i >> 1] = image.readUInt16LE(i);
const tx = 0x800, rx = 0x900, argument = 0xa00, result = 0xb00;
const terminal = 0xffff;
function boot() {
    const cpu = new CPU(words, 16384);
    cpu.dataView.setUint16(0x2643, tx, true); // OEM pucTxCommandBuffer
    cpu.data[cpu.SP] = 0xff; cpu.data[cpu.SP - 1] = 0xff; cpu.SP -= 2;
    return cpu;
}
function run(cpu, entry, stop, mocks = {}, budget = 20000) {
    cpu.pc = entry >> 1;
    for (let steps = 0; steps < budget; steps++) {
        const addr = cpu.pc * 2;
        if (addr === stop) return;
        if (mocks[addr]) {
            mocks[addr]();
            cpu.pc = 0x28d2 >> 1; // stock RET, return from simulated peripheral
        }
        avrInstruction(cpu);
    }
    throw Error('OEM cycle budget exhausted at byte ' + (cpu.pc * 2).toString(16));
}
const transcript = execFileSync(testExe, ['--vectors'], { encoding:'utf8' });
const vectors = new Set(transcript.split('\n').filter(s => /^[<>] [a-f0-9]+$/.test(s)));
const compared = new Set();
for (const vector of vectors) {
    const [direction, hex] = vector.split(' ');
    const data = Buffer.from(hex, 'hex');
    const cpu = boot();
    const u16 = (reg, value) => cpu.dataView.setUint16(reg, value, true);
    const u32 = (reg, value) => cpu.dataView.setUint32(reg, value, true);
    const op = data[0] === 2 ? 0x1003 : data.readUInt16LE(1);
    if (direction === '>') {
        let entry;
        switch (op) {
        case 0x1001:
            entry = 0x14984; u32(22, 2); u32(18, 1); u32(14, 6);
            break;
        case 0x1008:
            entry = 0x146ec; u32(22, 4); u16(20, argument);
            u16(14, argument + 4); u32(argument, 8); u32(argument + 8, 5000);
            break;
        case 0x1004:
            entry = 0x14692; u32(22, 3); u16(20, argument); u32(16, 16);
            break;
        case 0x1003: {
            const payload = data.subarray(21);
            entry = 0x14d54; u32(22, 3); u16(20, argument); u32(16, payload.length);
            cpu.data.set(payload, argument);
            break;
        }
        case 0x100b:
            entry = 0x148ec; u32(22, 3);
            break;
        default:
            // The outbound bind/listen/accept/setsockopt wrappers are absent.
            // The native test independently checks the TI driver's command ABI.
            continue;
        }
        u16(0x2659, 1); // one real shared TX credit available
        run(cpu, entry, 0x12d3e, {0x19d94: () => u32(22, 0)});
        if (op === 0x1003) {
            assert.equal(cpu.dataView.getUint16(0x2659, true), 0);
            assert.equal(cpu.dataView.getUint32(0x2661, true), 1);
        }
        assert.equal(cpu.dataView.getUint16(24, true), tx);
        assert.equal(cpu.dataView.getUint16(22, true), data.length);
        assert.deepEqual(Buffer.from(cpu.data.subarray(tx + 5, tx + 5 + data.length)), data);
    } else {
        u16(0x263f, 1); u16(0x2641, rx);
        cpu.data.set(data, rx); cpu.data.fill(0xa5, result, result + 32);
        u16(24, op); u16(22, result);
        run(cpu, 0x13e20, 2 * terminal, {
            0x19d94: () => u32(22, 0), // millis
            0x12c32: () => {},          // radio IRQ poll: event is already ready
            0x129c6: () => {},          // resume SPI after reading one event
        });
        assert.equal(cpu.dataView.getUint16(0x263d, true), 0);
        assert.equal(cpu.dataView.getUint16(0x263f, true), 0);
        assert.deepEqual(Buffer.from(cpu.data.subarray(result, result + data.length - 5)),
                         data.subarray(5));
    }
    compared.add(direction + op.toString(16));
}
assert.equal(compared.size, 14); // five retained send wrappers, nine event opcodes
function receiveIRQ(frame, expected) {
    const cpu = boot();
    cpu.dataView.setUint16(0x263d, expected, true);
    cpu.data.set(frame, rx);
    cpu.dataView.setUint16(24, rx, true);
    run(cpu, 0x1550e, 2 * terminal, {0x129c6: () => {}});
    return cpu;
}
const accept = Buffer.from('04051019000100000003000000' + '00'.repeat(16), 'hex');
for (const expected of [0, 0x1005, 0x1001]) {
    const cpu = receiveIRQ(accept, expected);
    assert.equal(cpu.dataView.getUint16(0x263f, true), 1);
    assert.equal(cpu.dataView.getUint16(0x2641, true), rx);
}
// One handle reclaimed two chip TX credits. The OEM IRQ callback
// consumes this notification without displacing the outstanding select.
const credits = receiveIRQ(Buffer.from('0400410700010000000200', 'hex'), 0x1008);
assert.equal(credits.dataView.getUint16(0x263f, true), 0);
assert.equal(credits.dataView.getUint16(0x263d, true), 0x1008);
assert.equal(credits.dataView.getUint16(0x2659, true), 2);
assert.equal(credits.dataView.getUint32(0x2665, true), 2);
for (const code of ['03', '0e', '0f']) {
    const inactive = receiveIRQ(Buffer.from('04'+code+'10090103000000c7ffffff', 'hex'), 0x1008);
    assert.equal(inactive.dataView.getUint16(0x263f, true), 0);
    assert.equal(inactive.dataView.getUint32(0x2655, true), 1);
    assert.equal(inactive.dataView.getUint32(0x09be, true), 8);
}

// An original synchronous caller with no event does not treat busy as a
// retry. It polls indefinitely unless all three diagnostic state bits allow
// the 3001 ms timeout. The timeout itself leaves the expected opcode set.
for (const diagnostic of [false, true]) {
    const cpu = boot();
    cpu.dataView.setUint16(24, 0x1001, true);
    cpu.dataView.setUint16(22, result, true);
    cpu.dataView.setUint32(result, 0x12345678, true);
    cpu.data[0x247b] = diagnostic ? 1 : 0;
    cpu.data[0x247d] = 1;
    const endObservation = Symbol('four complete polls observed');
    let polls = 0;
    const observe = () => run(cpu, 0x13e20, 2 * terminal, {
        0x19d94: () => cpu.dataView.setUint32(22, polls ? 3001 : 0, true),
        0x12c32: () => { if (++polls === 5) throw endObservation; },
    });
    if (diagnostic) observe();
    else assert.throws(observe, err => err === endObservation);
    assert.equal(cpu.dataView.getUint32(result, true), diagnostic ? 7 : 0x12345678);
    assert.equal(cpu.dataView.getUint16(0x263d, true), 0x1001);
}

// Execute the board-specific constructor and installation of the vendor host
// callbacks. Only the allocation return is stubbed; pin numbers and attached
// callbacks come from the original application image.
const pins = boot();
pins.dataView.setUint16(24, tx, true);
run(pins, 0x1193a, 2 * terminal, {0x1a7fc: () => pins.dataView.setUint16(24, result, true)});
assert.deepEqual(Array.from(pins.data.slice(0x24b4, 0x24b7)), [20, 2, 15]);
pins.data[0x2b] = 0xff; // PORTD
run(pins, 0x12ed6, 0x12f0a, {0x19db0: () => {
    assert.equal(pins.dataView.getUint32(22, true), 500);
}});
assert.equal(pins.data[0x2a] & 0x80, 0x80); // DDRD7 is an output
assert.equal(pins.data[0x2b] & 0x80, 0);    // initialized low
assert.equal(pins.data[0x27] & 0x10, 0x10); // DDRC4, radio CS output
assert.equal(pins.data[0x24] & 4, 0);       // DDRB2, radio IRQ input
assert.equal(pins.data[0x25] & 4, 4);       // IRQ pull-up enabled
run(pins, 0x1263a, 0x1266a);
assert.equal(pins.dataView.getUint16(0x2653, true) * 2, 0x1290a);
assert.equal(pins.dataView.getUint16(0x264d, true) * 2, 0x128f2);
assert.equal(pins.dataView.getUint16(0x264f, true) * 2, 0x129ae);
assert.equal(pins.dataView.getUint16(0x2651, true) * 2, 0x12922);
// Top-level invocations and the indirect low-level host callbacks above
// use the same code. A replacement at the function entry would acquire or
// release the foreground lease from inside an existing SPI transaction.
function directCalls(target) {
    const sites = [];
    for (let i=0; i+1<words.length; ++i) {
        if (words[i] === 0x940e && words[i+1] === target/2) sites.push(i*2);
    }
    return sites;
}
assert.deepEqual(directCalls(0x129ae), [0xbac4,0xbfaa,0xc376,0xcbcc,0xd098,0xd2d4,
                                       0xd536,0xd898,0xdafe,0xde02,0xe246]);
assert.deepEqual(directCalls(0x12922), [0xbad2,0xbfb6,0xc37e,0xcbde,0xd1a6,0xd2e0,
                                       0xd5e4,0xd912,0xd94e,0xdb08,0xde0c,0xe2c0]);
const levels = [0, 0, 1], writes = [];
pins.data[pins.SP] = pins.data[pins.SP - 1] = 0xff;
pins.SP -= 2;
run(pins, 0x15388, 2 * terminal, {
    0x1a000: () => writes.push([pins.data[24], pins.data[22]]),
    0x1a066: () => {
        assert.equal(pins.data[24], 2);
        assert(levels.length);
        pins.dataView.setUint16(24, levels.shift(), true);
    },
    0x19bb8: () => {}, // detachInterrupt for the radio
});
assert.deepEqual(writes, [[15, 0]]);
assert.equal(levels.length, 0);
assert.equal(pins.dataView.getUint16(0x2643, true), 0);

// At the W5100 read routine's two digitalWrite calls, the original image
// drives the very same logical pin first high and then low. No SPI traffic
// or physical board is required to check these two arguments.
for (const [entry, stop, level] of [[0x19068,0x19070,1], [0x1909c,0x190a4,0],
                                    [0x1921e,0x19226,1], [0x19252,0x1925a,0]]) {
    const cpu = boot();
    run(cpu, 0x19430, 0x1943a);
    assert.equal(cpu.data[0x2a] & 0xc0, 0xc0);
    cpu.data[0x2b] = level ? 0x35 : 0xb5;
    run(cpu, entry, stop);
    assert.equal(cpu.data[0x2b], level ? 0xb5 : 0x35);
}
// An ordinary step can reach the stock Wi-Fi client on the very next pass.
// Exercise the two periodic boundaries and an asynchronous filter removal.
for (const [elapsed,stop] of [[425000,0xecee],[425001,0x3c68]]) {
    const c=boot(); c.data[0x230c]=1; c.data[0x089b]=1;
    c.dataView.setUint16(28,0x3700,true);
    c.dataView.setUint32(0x3700+87,123,true);
    run(c,0xf660,stop,{
        0x19d94:()=>c.dataView.setUint32(22,123+elapsed,true),
        0x4caa:()=>{}
    });
}
for (const [elapsed,last,hit] of [[60000,1,false],[60001,1,true],[0,0,true]]) {
    const c=boot(); c.dataView.setUint32(0x23d7,last,true);
    run(c,0xe34e,hit?0xe3a0:0xe614,{
        0x19d94:()=>c.dataView.setUint32(22,last+elapsed,true)
    });
}
{
    const c=boot(); let reads=0;
    run(c,0xef2a,0xba96,{
        0x1a066:()=>{assert.equal(c.data[24],23);reads++;c.dataView.setUint16(24,0,true);},
        0x19db0:()=>{},0x3e7a:()=>{},0x4caa:()=>{},
    });
    assert.equal(reads,2); assert.equal(c.dataView.getUint16(22,true),8);
}
require('./cc3000_hot_brew.cjs')({boot,run,image});
console.log('Unmodified 1.1.14 matched five TX routines, nine completion handlers, RX/credit accounting, WLAN shutdown and shared GPIO evidence.');
require('./cc3000_display.cjs')({ words, boot, run, avrBin });
