'use strict';

const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } = require('node:fs');
const os = require('node:os');
const path = require('node:path');

module.exports = function verifyDisplayHandoff({ words, boot, run, avrBin }) {
    // Model only text rasterization and the external SPI signals. Both real
    // timer display paths, SPI mode changes, and the stock flush execute.
    function fixture() {
        const cpu = boot(), bytes = [], drawings = [];
        cpu.dataView.setUint16(0x1693+33, 0x25, true);
        cpu.dataView.setUint16(0x1693+35, 0x25, true);
        cpu.data[0x1693+39] = 1;
        cpu.data[0x1693+40] = 2;
        cpu.data[0x2390] = 1;
        cpu.data[0x2395] = 1;
        cpu.data[0x4c] = 0x54; // active radio SPI mode 1
        cpu.data[0x4d] = 0x80; // external SPI byte completes
        cpu.writeHooks[0x4e] = value => { bytes.push(value); return true; };
        cpu.readHooks[0x4e] = () => { throw Error('Display consumed a data-in byte'); };
        const draw = value => {
            drawings.push(value);
            cpu.data.fill(value, 0x266a, 0x266a+1024);
        };
        return {cpu, bytes, drawings, mocks:{0x7f0e: () => draw(0x15), 0x4f50: () => draw(0x3a)}};
    }
    function supervisor(f) {
        run(f.cpu, 0x10f60, 0x10f64, f.mocks, 400000);
        assert.deepEqual(f.drawings, [0x15, 0x3a]);
        assert.equal(f.cpu.dataView.getUint16(0x2397, true), 0); // original post-display work
    }
    const baseline = fixture();
    supervisor(baseline);
    const count = 6 + 1024 * 4; // 6 mode/window commands, 4096 expanded pixel bytes
    assert.equal(baseline.bytes.length, count * 2);
    assert.equal(baseline.cpu.data[0x4c], 0x50);
    if (!avrBin) return;

    // Place only the handoff in otherwise unused test flash/SRAM. The
    // two in-memory jumps do not create a flashable image or package.
    const temp = mkdtempSync(path.join(os.tmpdir(), 'zymatic-wifi-display-'));
    try {
        const elf = path.join(temp, 'guard.elf'), bin = path.join(temp, 'guard.bin');
        const base = 0x1c000;
        const tool = (name, args) => execFileSync(path.join(avrBin, name), args, {encoding:'utf8'});
        // Use the same pinned compact HTTP engine as the compact board
        // builder, exercising its real parser, key check and flash website.
        const build = readFileSync(path.resolve(__dirname,'../../tools/compact_build.py'),'utf8');
        const revision = build.match(/^SOURCE_REVISION = "([0-9a-f]{40})"$/m)[1];
        mkdirSync(path.join(temp,'site'));
        for (const file of ['zymatic_api.c','zymatic_api.h','site/zymatic_site.h']) {
            const blob = revision+':firmware/zymatic/modified/'+file;
            writeFileSync(path.join(temp,file),execFileSync('git',['show',blob],{cwd:__dirname}));
        }
        tool('avr-gcc', ['-mmcu=atmega1284p', '-Os', '-Wall', '-Wextra', '-Werror', '-nostartfiles',
            '-fno-jump-tables','-fno-tree-switch-conversion','-DZYMATIC_WIFI_COMPACT','-I'+temp,
            '-nostdlib', '-Wl,-Ttext=0x1c000,-Tbss=0x803300', path.resolve(__dirname, '../cc3000_spi_bus.c'),
            path.resolve(__dirname, '../cc3000_spi.c'), path.resolve(__dirname, '../cc3000_broker.c'),
            path.resolve(__dirname, '../cc3000.c'), path.resolve(__dirname,'../cc3000_board.c'),
            path.resolve(__dirname,'../server.c'),path.resolve(__dirname,'../http_session.c'),
            path.join(temp,'zymatic_api.c'), '-o', elf, '-lc', '-lgcc']);
        tool('avr-objcopy', ['-O','binary','-j','.text',elf,bin]);
        const binary = readFileSync(bin), symbols = new Map();
        for (const match of tool('avr-nm', [elf]).matchAll(/^([\da-f]+) T (zym_(?:spi|cc3000_board)_\w+)$/gm))
            symbols.set(match[2], parseInt(match[1], 16));
        assert.equal(symbols.size, 16);
        assert(base+binary.length <= 2*words.length);
        for (let i = 0; i < binary.length; i+=2) words[(base+i)/2] = binary.readUInt16LE(i);
        for (const at of [0x5044, 0x8738]) {
            assert.equal(words[at/2], 0xb58c); // original IN r24, SPCR
            words[at/2] = 0x940c;             // JMP, 16-bit word address
            words[at/2+1] = symbols.get('zym_spi_request_display')/2;
        }
        const f = fixture();
        const call = name => {
            const sp = f.cpu.SP;
            f.cpu.data[sp] = 0xff; f.cpu.data[sp-1] = 0xff; f.cpu.SP -= 2;
            run(f.cpu, symbols.get('zym_spi_'+name), 0x1fffe, {}, 400000);
            assert.equal(f.cpu.SP, sp);
            return f.cpu.data[24];
        };
        f.cpu.data[0x5f] |= 0x80;
        assert.equal(call('take_radio'), 1);
        assert.equal(f.cpu.data[0x5f] & 0x80, 0x80);
        assert.equal(call('take_radio'), 0);
        f.cpu.data[0x5f] &= 0x7f; // Timer 3's interrupt context
        supervisor(f);
        assert.equal(f.cpu.data[0x5f] & 0x80, 0);
        assert.equal(f.bytes.length, 0);
        assert.equal(f.cpu.data[0x4c], 0x54);
        f.cpu.data[0x5f] |= 0x80;
        call('release_radio');
        assert.equal(call('take_radio'), 0); // waiting screen has priority
        call('flush_pending');
        assert.equal(f.cpu.data[0x4c], 0x50);
        assert.deepEqual(f.bytes, baseline.bytes.slice(count));
        call('flush_pending');
        assert.equal(f.bytes.length, count);
        assert.equal(call('take_radio'), 1);
        console.log('Timer 3 rendered twice during the radio lease. The original OLED received one complete, byte-identical latest frame after handoff.');
        require('./cc3000_spi.cjs')({fixture, supervisor, run, symbols});
    } finally {
        rmSync(temp, {recursive:true, force:true});
    }
};
