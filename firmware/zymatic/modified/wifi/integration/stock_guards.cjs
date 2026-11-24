'use strict';
// Execute composed 1.1.14 control-flow branches, including register/SREG and
// heap ownership. Only stock peripheral inputs are simulated on the local CPU.
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {CPU, avrInstruction} = require(require.resolve('avr8js', {paths:[__dirname+'/../../../reverse-engineered/tests']}));
const baseline = readFileSync(process.argv[2]);
const candidate = readFileSync(process.argv[3]);
const symbols = JSON.parse(readFileSync(process.argv[4]));
function machine(bin) {
    const code = Buffer.alloc(0x20000,0xff);
    bin.copy(code);
    const cpu = new CPU(new Uint16Array(code.buffer,code.byteOffset,code.length/2),16384);
    cpu.data.set(baseline.subarray(0x1b24a,0x1b24a+0x980),0x100);
    return cpu;
}
function exec(cpu,entry,dest,override=()=>{}) {
    cpu.pc=entry/2;
    for(let i=0;i<500000;i++) {
        const pc=cpu.pc*2;
        if(dest.has(pc)) return pc;
        if(override(pc,cpu)) cpu.pc=0x28d2/2;
        avrInstruction(cpu);
    }
    assert.fail('AVR instruction timeout at 0x'+(cpu.pc*2).toString(16));
}
if (['compact','extended'].includes(process.argv[5])) {
    require('./pause_controls.cjs')({machine,exec,baseline,candidate,symbols,profile:process.argv[5]});
    process.exit(0);
}
function prepare(cpu,at,handle,flags) {
    for (let reg=2;reg<32;reg++) cpu.data[reg]=(reg*9 + at) & 255;
    cpu.data[0x5f]=flags;
    cpu.dataView.setUint16(0x16bc,handle,true);
}
const guarded = [0xbab4,0xbf9e,0xc36a,0xcbbc,0xd08c,0xd0c8,0xd12e,0xd19a,0xd2c8,0xd45a,0xd52a,
    0xd54e,0xd580,0xd5d8,0xd88c,0xd8ac,0xd8d8,0xd906,0xd942,0xdaf2,0xddf2,0xe238];
let checks=0;
const state=symbols.zym_wifi_flags-0x800000;
assert(state>=0x2c8a);
for (const at of guarded) {
    for(const handle of [0,0x3456]) for(const down of [0,1]) for(const flags of [0,0x7b,0x80,0xff]) {
        const orig=machine(baseline), test=machine(candidate);
        prepare(orig,at,down?0:handle,flags); prepare(test,at,handle,flags);
        test.data[state]=0x12|down;
        // First execute just the load and OR; checking SREG proves the original
        // BREQ/BRNE's condition as well as non-condition status preservation.
        const stop=new Set([at+10]);
        assert.equal(exec(orig,at,stop),exec(test,at,stop));
        assert.deepEqual(orig.data.slice(0,32),test.data.slice(0,32));
        assert.equal(orig.data[0x5f],test.data[0x5f]);
        assert.equal(test.dataView.getUint16(0x16bc,true),handle);
        assert.equal(test.SP,orig.SP);
        avrInstruction(orig); avrInstruction(test); assert.equal(orig.pc,test.pc);
        checks++;
    }
}
console.log(checks+' stock null-guard register/flag/branch comparisons passed');
for(const [sites,old] of [[[0x3c86,0x588e],0x3ae0],[[0x9168,0x9bd4,0x9f3c,0xb26e],0x8880],[[0xc620,0x1024a],0xb6b2]]) {
    for (const at of sites) for(const down of [0,1]) {
        const cpu=machine(candidate); prepare(cpu,at,0x3456,0x81);
        cpu.data[state]=down;
        const sp=cpu.SP;
        assert.equal(exec(cpu,at,new Set([old,at+4])),down?at+4:old);
        assert.equal(cpu.SP,sp-(down?0:2));
        if(down) assert.equal(cpu.data[24],0);
        assert.equal(cpu.dataView.getUint16(0x16bc,true),0x3456);
    }
}
console.log('Stock client/configuration calls retain ordinary paths and suppress failed-radio access');

// A modeled CC3000 loss occurs while the encoder waits on the Home list.
// The saved device object is intentionally still allocated after the loss.
// Step through the actual recipe loading, selection and brewing entry point.
if (symbols.gate_bind) {
    const cpu=machine(candidate);
    const ram = k => symbols[k]-0x800000;
    cpu.data[ram('gate_bind')]=1;
    cpu.data[0x230c]=1;
    cpu.dataView.setUint16(0x16bc,0x3400,true);
    cpu.data[0x5f]=0x80;
    const sp=cpu.SP;
    let doubled=false, screens=0, clock=100;
    function retAt(entry) {
        const stack=cpu.SP;
        cpu.data[stack]=cpu.data[stack-1]=0xff;cpu.SP=stack-2;
        exec(cpu,entry,new Set([0x1fffe]),intercept);
        assert.equal(cpu.SP,stack);
    }
    function intercept(pc) {
        if (pc===0x19d94) {
            cpu.dataView.setUint32(22,clock+=5,true);
            return true;
        }
        // Stop on any original WLAN driver or W5100 access.
        if ((pc>=0x112ec && pc<0x155f0) || pc===0x10252) assert.fail('Network touched after loss at 0x'+pc.toString(16));
        if (pc===0x1122c) {cpu.data[24]=0;return true;}
        if (pc===0x81f0) {
            const title=Buffer.from(cpu.data.subarray(0x2010,cpu.data.indexOf(0,0x2010))).toString();
            if (!screens) {
                assert.equal(title,'Brew a Recipe');
                // Exercise the real button hook selected by OEM 0x831e.
                cpu.data[ram('gate_fault')]=1;
                retAt(candidate.readUInt16LE(0x8320)*2);
                assert.equal(cpu.data[state]&1,1);
                assert.equal(cpu.data[0x230c],0);
            } else if(screens===1) {
                assert.match(title,/American Brown Ale/);
                doubled=true;
            } else assert.fail('Unexpected loop through the physical menus');
            screens++;
            cpu.data[24]=0;
            return true;
        }
        if ([0xa386,0x44fa,0x1604e,0x8734].includes(pc)) return true;
    }
    // Raise an error if the website or either network driver toggles PD7.
    cpu.writeHooks[0x2b] = (v,old) => {
        assert.equal((v^old)&128,0,'Ethernet CS changed on a Wi-Fi-only brewer');
        cpu.data[0x2b]=v; return true;
    };
    exec(cpu,0x10660,new Set([0xf6fe]),intercept);
    assert.equal(screens,2); assert(doubled);
    assert.equal(cpu.dataView.getUint16(0x16bc,true),0x3400);
    assert.equal(cpu.data[0x1ec5],1);
    assert.equal(cpu.data[0x16be+21],9);
    assert(cpu.SP<sp);
    console.log('Physical Home → cached nine-step brew runner passed after simulated WLAN loss');
}

if (symbols.gate_bind) {
    const dispatch=[0x105fe,0x10610,0x10624,0x1062c,0x10632,0x10638];
    for(let selection=0;selection<dispatch.length;selection++) {
        const cpu=machine(candidate);
        const ram=k=>symbols[k]-0x800000;
        cpu.data[ram('gate_bind')]=1;
        cpu.data[0x230c]=1;
        cpu.data[0x5f]=0x80; cpu.data[0x6e]=1; cpu.data[0x45]=3;
        let presses=0,clock=10;
        const boundary=exec(cpu,0x10594,new Set(dispatch),pc=>{
            if(pc===0x81f0) {
                ++presses; assert.equal(presses,1);
                assert.equal(cpu.data[22],6);
                cpu.data[24]=selection;
                return true;
            }
            if(pc===0x19d94) {
                cpu.dataView.setUint32(22,clock+=5,true);
                return true;
            }
            if(pc===symbols.zym_cc3000_board_poll) {
                // The last transaction completed while the original 0xff
                // home dispatch redraws its rows. No second dial press needed.
                cpu.data[ram('gate_loan')]=1;
            }
            if(pc>=0x112ec && pc<0x155f0) assert.fail('OEM WLAN invoked before clean handoff');
        });
        assert.equal(boundary,dispatch[selection]);
        assert.equal(presses,1);
        assert(cpu.data[ram('radio_polls')]>0);
        assert.equal(cpu.data[state]&1,0);
    }
    console.log('All six original Home selections survive a pending Wi-Fi transaction and dispatch exactly once');
    for(let row=0;row<5;row++) {
        const cpu=machine(candidate);
        cpu.data[state]=1;
        exec(cpu,0x10494,new Set([0x10498]),pc=>{
            if(pc!==0x81f0) return false;
            const addr=cpu.dataView.getUint16(24,true);
            assert.equal(Buffer.from(cpu.data.subarray(addr,cpu.data.indexOf(0,addr))).toString(),'Restart for network');
            cpu.data[24]=row;return true;
        });
        assert.equal(cpu.data[24],row<2?4:row);
    }
    const cpu=machine(candidate);
    cpu.data[state]=1;
    assert.equal(exec(cpu,0x855e,new Set([0x8562,0x7d38])),0x8562);
    console.log('Failed-radio Settings preserve local choices; About skips the blocking network information query');
}

// With the actual board driver linked, let the stock Home list open the
// HTTP listener. Return one real HCI socket handle; stall the next physical
// SPI read at BIND. Ensure Ethernet CS is never selected, then route to the
// verified saved program without touching the failed radio again.
if (symbols.zym_spi_bind && !symbols.gate_bind) {
    const cpu=machine(candidate), u=cpu.data;
    const put16=(a,v)=>cpu.dataView.setUint16(a,v,true);
    u[0x27]=16; u[0x28]=16;
    u[0x24b4]=20; u[0x24b5]=2; u[0x24b7]=2;
    u[0x2523]=0x54; u[0x2524]=1; u[0x252d]=2;
    u[0x263a]=u[0x25b7]=0xde; u[0x263c]=1;
    put16(0x264b,0x11e48/2); put16(0x264d,0x128f2/2);
    put16(0x264f,0x129ae/2); put16(0x2651,0x12922/2);
    put16(0x2659,2); put16(0x2533,0x2535); put16(0x16bc,0x3400);
    u[0x3d]=3; u[0x45]=3; u[0x6e]=1;
    u[0x5f]=0x80; u[0x4c]=0x50;
    u[0x230c]=1; u[0x24ee]=6;
    let response=[],out=[],ready=false,answer=0,stalled=false,reading=false,clock=100,screens=0;
    const commands=[];
    cpu.writeHooks[0x2b]=(v,old)=>{
        assert.equal((v^old)&128,0,'Ethernet CS changed on a Wi-Fi-only brewer'); u[0x2b]=v;return true;
    };
    cpu.readHooks[0x46]=()=>(cpu.cycles>>>6)&255;
    cpu.readHooks[0x23]=()=> (u[0x28]&16) && !response.length ? 4 : 0;
    cpu.writeHooks[0x4e]=v=>{
        assert.equal(u[0x28]&16,0,'unexpected non-radio SPI write');
        if(!out.length) reading=v===3;
        out.push(v); ready=!stalled; answer=reading?(response.shift()??0):0;return true;
    };
    cpu.readHooks[0x4e]=()=>{ready=false;return answer;};
    cpu.readHooks[0x4d]=()=>(u[0x4d]&1)|(ready?128:0);
    cpu.writeHooks[0x4d]=v=>{u[0x4d]=v&1;return true;};
    cpu.writeHooks[0x28]=(v,old)=>{
        u[0x28]=v;
        if((v&16) && !(old&16) && out.length) {
            if(out[0]===1) {
                const hci=out.slice(5), op=hci[1]|(hci[2]<<8);
                commands.push(op);
                if(op===0x1001) response=[0,0,0,0,0,4,1,16,5,0,1,0,0,0];
                else if(op===0x1009) response=[0,0,0,0,0,4,9,16,5,0,0,0,0,0];
                else {
                    assert.equal(op,0x1002,'Bound radio sent '+JSON.stringify(out));
                    assert.deepEqual(hci.slice(18,20),[0,80]);
                    response=[0,0,0,0,0,4,2,16,5,0,0,0,0,0];
                    stalled=true;
                }
            }
            out=[];
        }
        return true;
    };
    function call(entry) {
        const sp=cpu.SP; u[sp]=u[sp-1]=255;cpu.SP-=2;
        exec(cpu,entry,new Set([0x1fffe]),intercept);
        assert.equal(cpu.SP,sp);
    }
    function intercept(pc) {
        if(pc===0x19d94) {cpu.dataView.setUint32(22,clock+=6,true);return true;}
        if(pc===0x1122c) {u[24]=0;return true;}
        if(pc===0x81f0) {
            if(!screens++) {
                for (let i=0;i<18 && !(u[state]&1);i++) call(candidate.readUInt16LE(0x8320)*2);
                assert.deepEqual(commands,[0x1001,0x1009,0x1002]);
                assert.equal(u[state]&1,1);
                assert.equal(u[0x230c],0);
            } else assert.match(Buffer.from(u.subarray(0x2010,u.indexOf(0,0x2010))).toString(),/American Brown Ale/);
            u[24]=0;return true;
        }
        if ([0xa386,0x44fa,0x1604e,0x8734].includes(pc)) return true;
    }
    call(0x129ae);
    exec(cpu,0x10660,new Set([0xf6fe]),intercept);
    assert.equal(screens,2);
    assert.equal(u[0x16be+21],9);
    assert.equal(cpu.dataView.getUint16(0x16bc,true),0x3400);
    assert.equal(u[0x28]&16,16);
    console.log('Original Home requested a CC3000 socket bound to port 80 and reached the offline brew runner after a stalled SPI byte; no PD7 transitions');
}

if (symbols.zym_wifi_pause_dialog && symbols.gate_bind)
    require('./pause_controls.cjs')({machine, exec, baseline, candidate, symbols});
