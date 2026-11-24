'use strict';
const assert = require('node:assert/strict');

module.exports = function verifyBoardSPI({ fixture, supervisor, run, symbols }) {
    const source = 0x3800, resultPtr = 0x3900, sizePtr = 0x39ff, stop = 0x1fffe;
    const command = [1,1,0x10,12,2,0,0,0,1,0,0,0,6,0,0,0];
    const send = [2,0x81,16,19,0,3,0,0,0,12,0,0,0,3,0,0,0,0,0,0,0,97,98,99];
    const socketEvent = [4,1,0x10,5,0,3,0,0,0];
    const dataEvent = [2,0x85,4,6,0,3,0,0,0,65,66];
    function setup() {
        const f = fixture(), {cpu} = f;
        const set16 = (a, n) => cpu.dataView.setUint16(a,n,true);
        const get16 = a => cpu.dataView.getUint16(a,true);
        const get32 = a => cpu.dataView.getUint32(a,true);
        const rf = {wire:[], incoming:[], rxLow:false, neverReady:false, stallAfter:Infinity};
        let last = 0, ready = false;
        const u = cpu.data;
        u[0x27] |= 16; u[0x28] |= 16; // configured PC4 and deasserted
        u[0x24b4] = 20; u[0x24b5] = 2; u[0x24b7] = 2;
        u[0x2523] = 0x54; u[0x2524] = 1; u[0x252d] = 2;
        u[0x263a] = u[0x25b7] = 0xde; u[0x263c] = 1;
        set16(0x264d, 0x128f2/2); set16(0x264f,0x129ae/2); set16(0x2651,0x12922/2);
        set16(0x2659,2); set16(0x2533,0x2535);
        u[0x3d] = 3; u[0x45] = 3; // other external IRQs enabled, Timer0 F_CPU/64
        u[0x5f] = 0x80; u[0x4c] = 0x50; u[0x4d] = 0;
        cpu.readHooks[0x46] = () => (cpu.cycles >> 6) & 255;
        cpu.readHooks[0x23] = () => {
            if ((u[0x28] & 16) || rf.neverReady) return rf.rxLow ? 0 : 4;
            if (rf.inject) { const callback = rf.inject; rf.inject = null; callback(); }
            return 0;
        };
        cpu.writeHooks[0x4e] = value => {
            if (u[0x28] & 16) { f.bytes.push(value); ready = true; return true; }
            rf.wire.push(value);
            last = rf.incoming.shift() || 0;
            ready = rf.wire.length <= rf.stallAfter;
            return true;
        };
        cpu.readHooks[0x4e] = () => {ready = false; return last;};
        cpu.readHooks[0x4d] = () => (u[0x4d]&1) | (ready ? 0x80 : 0);
        cpu.writeHooks[0x4d] = value => {u[0x4d] = value & 1; return true;};
        function callAt(addr) {
            const sp = cpu.SP;
            u[sp] = u[sp-1] = 255; cpu.SP -= 2;
            run(cpu, addr, stop, {}, 400000);
            assert.equal(cpu.SP, sp);
            return get16(24);
        }
        const call = name => callAt(symbols.get('zym_spi_'+name));
        callAt(0x129ae); // stock attachInterrupt selects INT2 and the OEM callback
        assert.equal(get16(0x2b1f),0x12b08/2); assert.equal(u[0x3d],7);
        function bind() {
            assert.equal(call('bind') & 255,1);
            assert.equal(get16(0x2b1f),symbols.get('zym_spi_record_irq')/2);
            assert.equal(u[0x263c],0);
        }
        function tx(frame) {
            u.set(frame,source);
            set16(24,0); set16(22,source); u[20] = frame.length;
            return call('submit');
        }
        function rx() {
            set16(24,resultPtr); set16(22,sizePtr);
            return call('receive');
        }
        return {f, cpu, rf, u, set16, get16, get32, bind, tx, rx, callAt, call};
    }
    // Compare the entire SPI write (prefix and pad included) to the original
    // normal-state SpiWrite. Fixtures use fresh CPUs for each generation.
    for (const frame of [command,send,send.slice(0,-1).map((v,i)=>i===3 ? v-1 : v)]) {
        const o = setup();
        o.u.set(frame,source+5); o.set16(24,source); o.set16(22,frame.length);
        o.callAt(0x12d3e);
        const t = setup();
        t.bind();
        const start = t.cpu.cycles;
        assert.equal(t.tx(frame),1);
        assert.deepEqual(t.rf.wire,o.rf.wire);
        assert.equal(t.u[0x28]&16,16); assert.equal(t.u[0x4c],0x50);
        assert.equal(t.u[0x3d],7);
        assert.equal(t.get16(0x2659),frame[0]===2 ? 1 : 2);
        assert.equal(t.get32(0x2661),frame[0]===2 ? 1 : 0);
        assert(t.cpu.cycles-start < 16000);
    }
    const maximum = [4,5,0x10,121,0,...new Array(120).fill(3)];
    for (const event of [socketEvent,dataEvent,maximum]) {
        const t = setup();
        t.bind();
        t.rf.rxLow = true;
        t.callAt(0x19ca2); // actual external IRQ2 vector calls recorder
        assert.equal(t.rf.wire.length,0);
        assert.equal(t.tx(command),0); // existing radio frame wins
        const stream = [0,0,0,0,0,...event];
        if (event[0]===2) stream.push(0); // OEM data padding for even payload
        const o = setup();
        o.set16(0x2527, 0x1550e/2);
        o.rf.rxLow = true; o.rf.incoming = stream.slice();
        o.callAt(0x12b08);
        t.rf.incoming = stream.slice();
        assert.equal(t.rx(),1,`receive ${event.length} HCI bytes, transferred ${t.rf.wire.length}`);
        assert.deepEqual(t.rf.wire,o.rf.wire);
        assert.equal(t.rf.wire.length,stream.length);
        assert(t.rf.wire.every(v => v===3));
        assert.equal(t.u[sizePtr],event.length);
        assert.equal(t.get16(resultPtr),0x253a);
        assert.deepEqual(Array.from(o.u.slice(0x253a,0x253a+event.length)),event);
        assert.deepEqual(Array.from(t.u.slice(0x253a,0x253a+event.length)),event);
        assert.equal(t.u[0x25b7],0xde);
        assert.equal(t.u[0x4c],0x50);
        assert.equal(t.rx(),0); assert.equal(t.get16(resultPtr),0);
        t.rf.rxLow = false;
        assert.equal(t.tx(command),0);
        t.call('release_frame');
        assert.equal(t.rx(),0);
    }
    const t = setup();
    t.bind();
    t.set16(0x2659,0);
    assert.equal(t.tx(send),0); assert.equal(t.rf.wire.length,0);
    t.set16(0x2659,2);
    t.rf.neverReady = true;
    const start = t.cpu.cycles;
    assert.equal(t.tx(send),2);
    assert(t.cpu.cycles-start < 17000);
    assert.equal(t.get16(0x2659),2); assert.equal(t.get32(0x2661),0);
    assert.equal(t.u[0x3d],3); assert.equal(t.u[0x5f]&0x80,0x80);
    assert.equal(t.u[0x28]&16,16); assert.equal(t.u[0x4c],0x50);
    assert.equal(t.call('bind')&255,0); assert.equal(t.tx(command),2);
    assert.equal(t.rf.wire.length,0);
    const partial = setup();
    partial.bind(); partial.rf.stallAfter = 3;
    partial.cpu.readHooks[0x46] = () => 0; // exercise a broken Timer0
    assert.equal(partial.tx(send),2); assert.equal(partial.rf.wire.length,4);
    assert.equal(partial.get16(0x2659),1); assert.equal(partial.get32(0x2661),1);
    assert.equal(partial.rx(),2); assert.equal(partial.tx(send),2);
    assert.equal(partial.rf.wire.length,4);
    const bad = setup();
    bad.bind(); bad.rf.rxLow = true; bad.rf.incoming = [0,0,0,0,0,2,0x85,0,255,255];
    assert.equal(bad.rx(),2); assert.equal(bad.u[sizePtr],0); assert.equal(bad.rf.wire.length,10);
    // Lend the real dispatcher to a stock socket() call. It builds the
    // HCI command, transmits it, synchronously polls and decodes the reply.
    const loan = setup(), brokerAddr = 0x33c0;
    function stockControl(which) {
        loan.set16(24,brokerAddr);
        return loan.call(which) & 255;
    }
    loan.bind();
    loan.set16(brokerAddr,2); // an outstanding HTTP transaction
    assert.equal(stockControl('yield_to_stock'),0);
    assert.equal(loan.u[0x263c],0);
    loan.set16(brokerAddr,0);
    assert.equal(stockControl('yield_to_stock'),1);
    assert.equal(loan.get16(0x2b1f),0x12b08/2);
    loan.set16(0x2643,0x25b8); loan.set16(0x2527,0x1550e/2);
    const packet = [0,0,0,0,0,...socketEvent];
    let csEdges = 0;
    loan.cpu.writeHooks[0x28] = (v, old) => {
        loan.u[0x28] = v;
        if ((v & 16) && !(old & 16)) {
            ++csEdges;
            if (csEdges === 1) {
                loan.rf.rxLow = true; loan.rf.incoming = packet.slice();
            } else loan.rf.rxLow = false;
        }
        return true;
    };
    function interruptWithDisplay(machine) {
        machine.rf.inject = () => {
            const {cpu,u} = machine;
            const regs=u.slice(0,32), pc=cpu.pc, sr=u[0x5f], sp=cpu.SP;
            u[0x5f] &= 0x7f;
            supervisor(machine.f);
            assert.equal(cpu.SP,sp);
            assert.equal(machine.f.bytes.length,0);
            assert.equal(machine.rf.wire.length,0);
            u.set(regs); u[0x5f]=sr; cpu.pc=pc;
        };
    }
    interruptWithDisplay(loan);
    loan.cpu.dataView.setUint32(22,2,true); // socket(AF_INET,SOCK_STREAM,IPPROTO_TCP)
    loan.cpu.dataView.setUint32(18,1,true);
    loan.cpu.dataView.setUint32(14,6,true);
    loan.callAt(0x14984);
    assert.equal(loan.get16(24),3); assert.equal(csEdges,2);
    assert.equal(loan.get16(0x263d),0);
    const shift=loan.cpu.writeHooks[0x4e];
    loan.cpu.writeHooks[0x4e]=v=>{
        assert.equal(loan.u[0x3d]&4,0); // stock radio IRQ cannot preempt OLED
        return shift(v);
    };
    loan.call('flush_pending');
    assert.equal(loan.f.bytes.length,4102); assert.equal(loan.u[0x3d],7);
    loan.cpu.writeHooks[0x4e]=shift;
    loan.callAt(0x12922); // normal high-level stock exit: callback is now NULL
    assert.equal(loan.get16(0x2b1f),0); assert.equal(loan.u[0x3d],7);
    assert.equal(stockControl('resume_from_stock'),1);
    assert.equal(loan.u[0x263c],0);
    assert.equal(loan.get16(brokerAddr),0);
    assert.equal(loan.tx(command),1);
    assert.equal(stockControl('yield_to_stock'),1);
    stockControl('stock_restart');
    assert.equal(stockControl('resume_from_stock'),0);
    assert.equal(loan.get16(brokerAddr),3);
    assert.equal(loan.get16(0x2b1f),0x12b08/2);
    // Run the physical recipe-sync dispatcher. An unavailable TCP operation
    // must use its original error path; a busy broker must not enter that
    // path or drop the action. Its caller must schedule arbitration first.
    const menu = setup();
    menu.callAt(0x12922);
    menu.bind();
    menu.set16(24,brokerAddr); assert.equal(menu.call('yield_to_stock')&255,1);
    menu.u[0x089d] = 1; menu.set16(0x16bc,0x1800);
    const state = [];
    const menuIO = {
        0x5ebc: () => {menu.u[24]=5;},
        0x4342: () => {menu.u[24]=1;},
        0x2cd2: () => {menu.u[24]=1;},
        0x3228: () => {}, 0x1604e: () => {}, 0x5040: () => {}, 0x19db0: () => {},
        0x11562: () => {state.push(menu.u[0x089d]); menu.u[24]=0;},
        0x6da8: () => {state.push(menu.u[0x089d]);},
    };
    run(menu.cpu,0xdade,stop,menuIO);
    assert.deepEqual(state,[0,0]); assert.equal(menu.u[0x089d],1);
    assert.equal(menu.u[0x263c],0); assert.equal(menu.get16(0x2b1f),0);
    menu.set16(24,brokerAddr); assert.equal(menu.call('resume_from_stock')&255,1);
    // Enter the actual timer display path during CS assertion. The CPU
    // preserves registers at the asynchronous boundary as the AVR ISR does.
    const concurrent = setup();
    concurrent.bind();
    interruptWithDisplay(concurrent);
    assert.equal(concurrent.tx(command),1);
    assert.equal(concurrent.tx(command),0); // waiting OLED has priority
    concurrent.call('flush_pending');
    assert.equal(concurrent.f.bytes.length,4102);
    assert.equal(concurrent.tx(command),1);
    console.log('Bounded AVR SPI transfers matched OEM writes, handled deferred INT2 and OLED traffic, accounted for credits and quarantined failed transactions.');
    require('./cc3000_board.cjs')({setup,symbols,run,recipe: (menu,serve) => syncMenu(menu,run,stop,serve)});
};

function syncMenu(menu,run,stop,serve) {
    // The TCP façade supplies a normal Hub-format menu response. The
    // completed request, stream framing, tokenizer and brew-bank writes
    // all execute in the original foreground. The physical brewer is idle.
    const id = '0123456789abcdef0123456789abcdef';
    const wire = Buffer.from('#Bench Pale/'+id+'/Mash,153,60,1,0/Boil,203,60,0,0/|#');
    const ramString = addr => Buffer.from(menu.u.slice(addr,menu.u.indexOf(0,addr))).toString('ascii');
    const crt = new Uint8Array(menu.cpu.progMem.buffer,0x1b24a,0x980);
    menu.u.set(crt,0x100);
    menu.set16(0x16bc,0x1800);
    menu.set16(0x2643,0x25b8); menu.set16(0x2527,0x1550e/2);
    menu.u.set(Buffer.from('TEST-USER\0'),0x1672);
    menu.u[menu.cpu.SP]=menu.u[menu.cpu.SP-1]=255; menu.cpu.SP-=2;
    menu.u[24]=0;
    let position = 0, closed = false, joined = false;
    const request = [];
    serve((hci, op, evt, int, waiting) => {
        switch (op) {
        case 0x1001: waiting.push(evt(op,...int(2))); break;
        case 0x1007: assert.equal(hci[4],2); joined=true; waiting.push(evt(op,...int(0))); break;
        case 0x1003:
            assert.equal(hci[5],2); request.push(...hci.slice(21,21+hci[13]));
            waiting.push(evt(0x4100,1,0,0,0,1,0),evt(op,...int(2),...int(hci[13])));
            break;
        case 0x1008:
            waiting.push(evt(op,...int(position < wire.length ? 1 : 0),
                ...int(position < wire.length ? 4 : 0),...Array(8).fill(0)));
            break;
        case 0x1004: {
            const chunk=wire.slice(position,position+hci[8]); position+=chunk.length;
            waiting.push(evt(op,...int(2),...int(chunk.length),...int(0)));
            if (chunk.length) waiting.push([2,0x85,4,4+chunk.length,0,...int(2),...chunk]);
            break;
        }
        case 0x100b:
            assert.equal(hci[4],2);closed=true;waiting.push(evt(op,...int(0)));break;
        default: throw Error('Unexpected OEM HCI operation '+op.toString(16));
        }
    });
    run(menu.cpu,0xdade,stop,{
        0x5ebc: () => {menu.u[24]=5;}, 0x2cd2: () => {menu.u[24]=1;},
        0x3228: () => {}, 0x1604e: () => {}, 0x5040: () => {}, 0x19db0: () => {},
        0x4342: () => {menu.u.set(Buffer.from('123456789ABC\0'),menu.get16(24)); menu.u[24]=1;},
        0x1abd4: () => {}, 0x1abce: () => {}, 0x1a914: () => {}, 0x1aaae: () => {},
        0x2ea2: () => {}, 0x19d94: () => menu.cpu.dataView.setUint32(22,0,true),
        0x1a7fc: () => {menu.set16(24,0x3030);}, // original C++ Client allocation
        0x6da8: () => {throw Error('normal recipe entered the network error path');},
    },4000000);
    serve(null);
    assert.equal(position,wire.length); assert(closed && joined);
    assert.match(Buffer.from(request).toString('ascii'),/SyncUser\?user=TEST-USER&machine=123456789ABC/i);
    assert.equal(menu.u[0x1ec5],1); assert.equal(ramString(0x16be),'Bench Pale');
    assert.equal(ramString(0x1d76),id); assert.equal(menu.u[0x16be+21],2);
    assert.equal(menu.u[0x089d],1);
}
