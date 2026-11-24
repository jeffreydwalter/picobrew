'use strict';

// Standalone witnesses for the lifetime and timing of original hot-side
// transfers. A future cooperative sender must preserve the records and the
// physical output sequence for these same cases.
const assert = require('node:assert/strict');
module.exports = function({boot,run,image}) {
    const c=boot(), stack=c.SP, ports=[];
    c.data.set(image.subarray(0x1b24a,0x1b24a+0x980),0x100);
    const regs=c.dataView;
    function word(addr) {return regs.getUint16(addr,true);}
    function u32(addr) {return regs.getUint32(addr,true);}
    c.data.set(Buffer.from('0123456789abcdef0123456789abcdef\0'),0x23f6);
    regs.setFloat32(22,72,true); regs.setFloat32(18,153,true);
    regs.setUint32(0x23d7,1,true);
    let sample, held;
    run(c,0xe304,0x1fffe,{
        0x19d94:()=>regs.setUint32(22,60002,true),
        0x4862:()=>{c.data[24]=0;},
        0x4caa:()=>{},0x4c52:()=>{},
        0xe160:()=>{
            assert.equal(u32(0x23d7),60002);
            assert.equal(word(24),0x23f6);
            held=word(22);
            assert(held>c.SP && held<stack); // r22:r23 is a live local buffer.
            sample=Buffer.from(c.data.subarray(held,c.data.indexOf(0,held))).toString();
            assert(sample.length>0);
            c.data[24]=0; // Stock sender reports failure.
        }
    },160000);
    assert(held); assert(c.SP>held);
    assert.equal(u32(0x23d7),60002); // A false sender return does not cause a retry.
    // Prove the stock request's reset point can be consumed after this call
    // only if the transport copies it while the original frame is live.
    c.data.fill(0,held,held+sample.length);
    assert.equal(c.data[held],0);

    // 0x6c06 is the Wi-Fi sample wrapper. Crossing its suppression threshold
    // directly returns failure, without retaining the call for a later poll.
    for (const [enabled, counter, expected] of [[0,4,true],[1,3,true],[1,4,false]]) {
        const transport=boot();
        transport.data[0x247b]=enabled;
        transport.data[0x2476]=counter;
        let submitted=false;
        run(transport,0x6c06,0x1fffe,{0x680e:()=>{
            submitted=true;
            transport.data[24]=1;
        }});
        assert.equal(submitted,expected);
        assert.equal(transport.data[24],Number(expected));
    }

    const f=boot(); const fregs=f.dataView; let present=false, afterNotice=false;
    fregs.setUint16(24,0x23f6,true);
    const events=[];
    run(f,0xbdda,0x1fffe,{
        0x1a066:()=>{
            if(f.data[24]===23) fregs.setUint16(24,present?1:0,true);
            else fregs.setUint16(24,1,true); // Energized original outputs.
        },
        0x1a000:()=>ports.push([f.data[24],f.data[22]]),
        0x19db0:()=>{},
        0x4caa:()=>{},0x4c52:()=>{},0x3f26:()=>{},
        0x1604e:()=>{},0x4f50:()=>{},0x15e46:()=>{},
        0xba96:()=>{
            const code=fregs.getUint16(22,true);
            if (!afterNotice) {
                assert.deepEqual(ports,[[4,0],[13,0],[24,0],[25,0]]);
                afterNotice=true;
                present=true; // Filter is already reinserted before the wait polls it.
            }
            events.push(code); f.data[24]=0;
        },
        0x1122c:()=>assert.fail('Filter wait unexpectedly reached the normal brew button poll'),
    },150000);
    assert.deepEqual(events,[8,208]); assert.equal(f.data[24],1);
    assert.deepEqual(ports.slice(4),[[4,1],[13,1],[24,1],[25,1]]);
    console.log('OEM failed-send sample lifetime and two filter reports verified with physical shutdown before the notices.');
};
