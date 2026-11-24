'use strict';
const assert = require('node:assert/strict');

module.exports = function ({ machine, exec, candidate, baseline, symbols, profile = 'fixture' }) {
    const ram = k => symbols[k] - 0x800000;
    const state = ram('zym_wifi_flags');
    const scripted = profile === 'fixture';
    // Drive stock instructions that deenergize the outputs before this recipe
    // pause. None of these calls are supplied by the added network code.
    for (const image of [baseline, candidate]) {
        const c = machine(image), writes = [];
        exec(c, 0xf954, new Set([0xf988]), pc => {
            if (pc === 0x1604e || pc === 0x19db0) return true;
            if (pc === 0x1a000) { writes.push([c.data[24],c.data[22]]); return true; }
        });
        assert.deepEqual(writes, [[4,0],[13,0],[24,0],[25,0]]);
    }
    for (const fault of [false,true]) for (const choice of [0,1]) {
        const c=machine(candidate);
        c.data[0x230c]=1; c.data[0x5f]=0x80;
        c.data[0x6e]=1; c.data[0x45]=3;
        if (scripted) c.data[ram('gate_bind')]=1;
        c.data[state]=4; // Stock owns the radio during the step preceding route 6.
        const stack=c.SP;
        let clock=0, press=false, polls=0, takeAfter=Infinity;
        function io(pc) {
            if (pc===0x19d94) { c.dataView.setUint32(22,clock+=5,true); return true; }
            if (pc===0x1122c) { c.data[24]=Number(press); return true; }
            if (!scripted && pc===symbols.zym_spi_resume_from_stock) { c.data[24]=1; return true; }
            if (!scripted && pc===symbols.zym_spi_yield_to_stock) {
                c.data[24]=Number(polls>=takeAfter && !fault); return true;
            }
            if (pc===symbols.zym_cc3000_board_poll) {
                assert.equal(c.data[10],4); // Read-only, busy recipe pause.
                polls++;
                if (!scripted) {
                    if (fault && polls>=takeAfter) c.data[ram('candidate_broker')]=3;
                    return true;
                }
                if (polls>=takeAfter) c.data[ram(fault ? 'gate_fault':'gate_loan')]=1;
            }
            if (pc>=0x112ec && pc<0x15570) assert.fail('Unexpected stock Wi-Fi I/O during pause at '+pc.toString(16));
        }
        exec(c,0xfa28,new Set([0xfa2c]),io);
        assert.equal(c.SP,stack);
        assert(c.data[state]&64);
        assert.equal(c.data[24],0); assert(polls>0);
        press=true;
        exec(c,0xfa28,new Set([0xfa2c]),io);
        assert.equal(c.data[24],1); // Physical latch has priority.
        takeAfter=polls+1;
        let inDialog=false, reads=0;
        const boundary=exec(c,0xfae6,new Set([0xfc58,0xfaf6]),pc=>{
            if (pc===0x7f9a) {
                assert.deepEqual([c.data[24],c.data[22],c.data[20]],[2,7,0]);
                assert(!inDialog); inDialog=true;
            }
            if (inDialog) {
                // Keep the real original draw/confirm loop and return. Stub
                // display hardware and provide a physical encoder/button.
                if (pc===0x111ce) { c.dataView.setUint16(24,choice,true); return true; }
                if (pc===0x1122c) {c.data[24]=Number(++reads>=2); return true;}
                if ([0x4f50,0x5dda,0x7f0e,0x15e46,0x111a8,0x111b0,0x111be,0x11240].includes(pc)) return true;
            }
            return io(pc);
        });
        assert(inDialog); assert(reads>=2); assert(polls>=takeAfter);
        assert.equal(c.SP,stack);
        assert.equal(boundary,choice===1?0xfc58:0xfaf6);
        assert.equal(c.data[state]&64,0);
        assert.equal(c.data[state]&1,Number(fault));
    }
    const c=machine(candidate);
    c.data[0x230b]=1; c.data[0x230c]=1; c.data[0x5f]=0x80;
    exec(c,0xfa28,new Set([0xfa2c]),pc=>{
        if(pc===0x1122c) {c.data[24]=0;return true;}
        if(pc===symbols.zym_wifi_network_poll) assert.fail('Wi-Fi hook changed wired pause');
    });
    assert.equal(c.data[state],0);
    const net=0x3300;
    const extended=profile==='extended';
    const key=extended ? symbols.provision_key & 0xffff : 0x3600;
    const uid=extended ? symbols.provision_uid & 0xffff : 0x3700;
    function invoke(name) {
        const stack=c.SP; c.data[stack]=c.data[stack-1]=0xff; c.SP-=2;
        exec(c,symbols[name],new Set([0x1fffe]));
        assert.equal(c.SP,stack);
    }
    let credential;
    if (extended) {
        credential=candidate.subarray(symbols.provision_key,symbols.provision_key+32).toString('hex');
    } else {
        c.data.fill(0x21,key,key+32);
        c.data.set(Buffer.from('0123456789abcdef0123456789abcdef\0'),uid);
        credential='21'.repeat(32);
    }
    function request(line,auth,body='') {
        c.data.fill(0,net,net+460);
        const headers=[line+' HTTP/1.0',...(auth?['Authorization: Bearer '+credential]:[]),
            ...(body?['Content-Length: '+Buffer.byteLength(body)]:[])];
        for (const ch of Buffer.from(headers.join('\r\n')+'\r\n\r\n'+body)) {
            c.dataView.setUint16(24,net,true); c.data[22]=ch; c.dataView.setUint16(20,key,true);
            invoke('zymatic_api_feed');
        }
        c.dataView.setUint16(24,net,true);
        if (extended) {
            c.dataView.setUint16(22,key,true); c.dataView.setUint16(20,uid,true);
            c.dataView.setUint32(16,72,true); c.data[14]=4;
        } else {
            c.dataView.setUint16(22,uid,true); c.dataView.setUint32(18,72,true); c.data[16]=4;
        }
        invoke('zymatic_api_reply');
        return Buffer.from(c.data.subarray(net,net+c.dataView.getUint16(net+396,true))).toString();
    }
    assert.match(request('GET /api/v1/status',false),/HTTP\/1.0 401/);
    assert.match(request('GET /api/v1/status',true),/"state":"recipe_pause","busy":true/);
    assert.match(request('GET /api/v1/display',true),/HTTP\/1.0 200/);
    if (extended) assert.match(request('POST /api/v1/commands',true,'{"command":"encoder","press":true,"display_revision":1}'),/HTTP\/1.0 405/);
    console.log('Output-off recipe pause retained both physical decisions after a clean or faulted radio handoff and reported the running program busy.');
};
