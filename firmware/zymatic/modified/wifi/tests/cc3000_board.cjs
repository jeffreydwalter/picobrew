'use strict';
const assert = require('node:assert/strict');

// End-to-end from the CC3000's pins to the pinned compact AVR HTTP engine.
// The test models the external network processor, not the application API.
module.exports = function verifyBoard({ setup, symbols, run, recipe }) {
    const t = setup();
    const b = 0x3430, radio = 0x3450, http = 0x3500, key = 0x3700, uid = 0x3750;
    const requestKey = Buffer.alloc(32,0x21);
    t.u.set(requestKey,key);
    t.u.set(Buffer.from('0123456789abcdef0123456789abcdef\0'),uid);
    t.set16(0x264b,0x11e48/2); // callback installed by stock setup
    function begin(f=t) {
        f.set16(24,b); f.set16(22,radio); f.set16(20,8080);
        return f.callAt(symbols.get('zym_cc3000_board_begin')) & 255;
    }
    function classify(frame, f=t) {
        f.u.set(frame,0x253a); f.set16(24,0x253a);
        f.callAt(0x13178); assert.equal(f.get32(22),1);
    }
    assert.equal(begin(),0);
    classify([4,1,0x80,1,0]);
    assert.equal(t.u[0x24ee],2); assert.equal(begin(),0);
    classify([4,0x10,0x80,21,0,...Array(20).fill(1)]);
    assert.equal(t.u[0x24ee],6); assert.equal(begin(),1);
    const waiting = [], replies = [], commands = [];
    const requests = ['GET / HTTP/1.0\r\n\r\n','GET /api/v1/status HTTP/1.0\r\n\r\n',
        'GET /api/v1/status HTTP/1.0\r\nAuthorization: Bearer '+requestKey.toString('hex')+'\r\n\r\n'];
    let bytes = [], output = [], start = 0, oemServer, faultAtRead = false, down = false;
    const retired = [];
    function evt(op, ...args) {
        return [4,op & 255,op >> 8,1+args.length,0,...args];
    }
    function int(n) { return [n,0,0,0]; }
    function command(frame) {
        const hci = frame.slice(5), op = hci[0]===1 ? hci[1] | hci[2]<<8 : 0x1003;
        if (oemServer) { oemServer(hci,op,evt,int,waiting); return; }
        commands.push(op);
        switch (op) {
        case 0x1001: waiting.push(evt(op,...int(1))); break;
        case 0x1002:
            assert.deepEqual(hci.slice(18,20),[0x1f,0x90]);
            // Simulated TCP listener binds the actual Hub-configured port.
            waiting.push(evt(op,...int(0))); break;
        case 0x1006: case 0x1009: waiting.push(evt(op,...int(0))); break;
        case 0x1005:
            assert(requests.length); bytes=Array.from(Buffer.from(requests.shift()));
            waiting.push(evt(op,...int(1),...int(3),...Array(16).fill(0)));
            break;
        case 0x1008:
            waiting.push(evt(op,...int(bytes.length ? 1 : 0),...int(bytes.length ? 8 : 0),...Array(8).fill(0)));
            break;
        case 0x1004: {
            const chunk=bytes.splice(0,16); assert(chunk.length);
            if (faultAtRead) {waiting.push(evt(0x8002)); faultAtRead=false; down=true;}
            waiting.push(evt(op,...int(3),...int(chunk.length),...int(0)));
            waiting.push([2,0x85,4,4+chunk.length,0,...int(3),...chunk]);
            break;
        }
        case 0x1003:
            assert.equal(hci[5],3);
            output.push(...hci.slice(21,21+hci[13]));
            waiting.push(evt(0x4100,1,0,0,0,1,0),evt(op,...int(3),...int(hci[13])));
            break;
        case 0x100b:
            if (down) { retired.push(hci[4]); bytes=[]; }
            else {
                assert.equal(hci[4],3); assert.equal(bytes.length,0);
                replies.push(Buffer.from(output).toString('ascii')); output=[];
            }
            waiting.push(evt(op,...int(0)));
            break;
        default: throw Error('Unexpected board HCI operation '+op.toString(16));
        }
    }
    function present() {
        t.rf.rxLow=waiting.length>0;
        if (t.rf.rxLow) {
            const event=waiting[0];
            t.rf.incoming = [0,0,0,0,0,...event];
            if ((event[0]===2) === Boolean(event.length&1)) t.rf.incoming.push(0);
        }
    }
    t.cpu.writeHooks[0x28] = (value,old) => {
        t.u[0x28]=value;
        if ((value&16) && !(old&16)) {
            const frame=t.rf.wire.slice(start); start=t.rf.wire.length;
            if (frame[0]===3) {
                assert(waiting.length); assert.equal(t.rf.incoming.length,0);
                waiting.shift();
            } else {
                assert.equal(frame[0],1); command(frame);
            }
            present();
        }
        return true;
    };
    let now=0, requestedLoan=false, menuRan=false;
    function tick() {
        t.set16(24,b); t.set16(22,radio); t.set16(20,http); t.set16(18,key); t.set16(16,uid);
        t.cpu.dataView.setUint32(12,++now,true); t.u[10]=0;
        t.callAt(symbols.get('zym_cc3000_board_poll'));
    }
    while (replies.length < 3 && now < 1800) {
        tick();
        // Interrupt the open web connection with an actual physical menu
        // sync request. The issued SELECT must complete before stock runs.
        if (!requestedLoan && commands.at(-1) === 0x1008 && waiting.length) {
            t.set16(24,b); assert.equal(t.call('yield_to_stock')&255,0);
            requestedLoan=true;
        } else if (requestedLoan && !menuRan && t.get16(b)===0 && !waiting.length) {
            t.set16(24,b); assert.equal(t.call('yield_to_stock')&255,1);
            recipe(t,server=>{oemServer=server;});
            t.set16(24,b); assert.equal(t.call('resume_from_stock')&255,1);
            menuRan=true;
        }
    }
    assert(menuRan);
    assert.equal(replies.length,3,'commands '+commands.slice(-8).map(x=>x.toString(16)));
    assert.match(replies[0], /HTTP\/1\.[01] 200 [\s\S]*<!doctype html>/i);
    assert.match(replies[1], /HTTP\/1\.[01] 401 /);
    assert.match(replies[2], /HTTP\/1\.[01] 200 [\s\S]*"device_uid":"0123456789abcdef0123456789abcdef"/);
    assert.equal(t.get32(0x2661),t.get32(0x2665)); assert.equal(t.get16(0x2659),2);
    assert.equal(t.u[0x25b7],0xde); assert.equal(t.u[0x263c],0);
    // Drop the station while a read is waiting for both its status and
    // its data. Retire the old descriptors without dispatching the request.
    requests.push('GET /api/v1/status HTTP/1.0\r\n\r\n'); faultAtRead=true;
    for(let i=0;i<70 && (retired.length<2 || waiting.length);i++) tick();
    assert.deepEqual(retired,[3,1]); assert.equal(t.get16(b),0);
    assert.equal(t.u[0x24ee]&6,0); assert.equal(t.u[0x3d]&4,4);
    assert.equal(output.length,0); assert.equal(replies.length,3);
    const idleWrites=commands.length;
    for(let i=0;i<5;i++) tick();
    assert.equal(commands.length,idleWrites);
    const lease=evt(0x8010,...Array(20).fill(1)), failed=lease.slice(); failed[4]=1;
    waiting.push(evt(0x8001),failed); present(); tick(); tick(); tick();
    assert.equal(commands.length,idleWrites);
    down=false; requests.push('GET /api/v1/status HTTP/1.0\r\n\r\n');
    waiting.push(lease); present();
    for(let i=0;i<180 && replies.length<4;i++) tick();
    assert.equal(replies.length,4); assert.match(replies[3],/HTTP\/1\.[01] 401 /);
    // Stock can disconnect and reconnect on its own synchronous stack.
    // Nobody may borrow its live socket numbers until the old listener retires.
    tick(); t.set16(24,b); assert.equal(t.call('yield_to_stock')&255,1);
    classify(evt(0x8002)); classify(evt(0x8001)); classify(lease);
    t.set16(24,b); assert.equal(t.call('resume_from_stock')&255,1);
    t.set16(24,b); assert.equal(t.call('yield_to_stock')&255,0);
    down=true;
    for(let i=0;i<10 && retired.length<3;i++) tick();
    assert.deepEqual(retired,[3,1,1]);
    down=false; requests.push('GET /api/v1/status HTTP/1.0\r\n\r\n');
    for(let i=0;i<180 && replies.length<5;i++) tick();
    assert.match(replies[4],/HTTP\/1\.[01] 401 /);

    const f=setup(); f.set16(0x264b,0x11e48/2); f.u[0x24ee]=6;
    assert.equal(begin(f),1);
    f.set16(24,b); assert.equal(f.call('yield_to_stock')&255,1);
    f.set16(24,b); f.call('stock_restart');
    assert.equal(begin(f),0); // Old callback still belongs to the previous boot.
    run(f.cpu,0x1263a,0x1266a); // Original init reinstalls the real callback.
    assert.equal(f.get16(0x264b),0x11e48/2);
    classify(evt(0x8002),f); assert.equal(begin(f),0);
    classify(evt(0x8001),f); assert.equal(begin(f),0);
    classify(lease,f); assert.equal(begin(f),1);
    function pollAt(time) {
        f.set16(24,b); f.set16(22,radio); f.set16(20,http); f.set16(18,key); f.set16(16,uid);
        f.cpu.dataView.setUint32(12,time,true); f.u[10]=0;
        f.callAt(symbols.get('zym_cc3000_board_poll'));
    }
    pollAt(0); assert.equal(f.get16(b),2);
    pollAt(3500); assert.equal(f.get16(b),3); assert.equal(f.u[0x3d]&4,0);
    f.set16(0x264b,0x11e48/2); f.u[0x3d]|=4;
    assert.equal(begin(f),0); // An unacknowledged command quarantines the physical link.
    for (const invalid of [evt(0x4100,1,0,0,0,9,0), evt(0x4100,2,0),
                           evt(0x8010,1), evt(0x8800)]) {
        const f=setup();
        f.set16(0x264b,0x11e48/2); f.u[0x24ee]=6;
        f.set16(24,b); f.set16(22,radio); f.set16(20,8080);
        assert.equal(f.callAt(symbols.get('zym_cc3000_board_begin'))&255,1);
        f.rf.rxLow=true; f.rf.incoming=[0,0,0,0,0,...invalid];
        f.set16(24,b); f.set16(22,radio); f.set16(20,http); f.set16(18,key); f.set16(16,uid);
        f.cpu.dataView.setUint32(12,0,true); f.u[10]=0;
        f.callAt(symbols.get('zym_cc3000_board_poll'));
        assert.equal(f.get16(b),3); assert.equal(f.u[0x3d]&4,0);
        assert.equal(f.get32(0x2665),0); assert.equal(f.get16(0x2659),2);
    }
    console.log('AVR board served the compact device website, required a key for status, returned the enrolled UID and processed original DHCP, credit and disconnect callbacks.');
};
