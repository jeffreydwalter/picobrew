'use strict';
// Compare pinned OEM instruction ranges with their linked replacements, using
// independent AVR8JS CPUs. Nothing is connected to a physical brewer.
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const {createRequire} = require('node:module');
const path = require('node:path');
const load = createRequire(path.resolve(__dirname, '../../../reverse-engineered/tests/package.json'));
const {CPU, avrInstruction} = load('avr8js');
const {avrInterrupt} = load('avr8js/dist/cjs/cpu/interrupt');

const [rawPath, candidatePath, sitesPath] = process.argv.slice(2);
if (!sitesPath) throw Error('Usage: node push_compare.cjs original.bin candidate.bin sites.txt');
const sites = readFileSync(sitesPath,'utf8').trim().split(/\r?\n/).map(line => {
    const match = /^\s*(?:0x)?([\da-f]+)\s+(?:0x)?([\da-f]+)\s*$/i.exec(line);
    if (!match) throw Error('Expected two hexadecimal byte addresses on each line: ' + line);
    const [start,end]=match.slice(1).map(x=>parseInt(x,16));
    assert(start>=0x100 && start<end && end<=0x20000 && !(start&1) && !(end&1));
    return {start,end};
});
assert(sites.length);
const push = reg => 0x920f + reg*16;
const pop = reg => 0x900f + reg*16;
const irqRegisters = [0,1,18,19,20,21,24,25,26,27,28,29,30,31];
// A synthetic ISR exercises hardware return-address pushes, temporary pushes,
// register clobbers and restoration. Its LDI instructions preserve SREG.
const irqCode = [...irqRegisters.map(push),0xe03c,0xe883,0xeece,0xe5d9,0xe6e5,0xe7f6,
                 ...[...irqRegisters].reverse().map(pop),0x9518];
function machine(filename) {
    const raw=readFileSync(filename);
    assert(raw.length && !(raw.length&1) && raw.length<=0x20000);
    const words=new Uint16Array(0x10000).fill(0xffff);
    for (let i=0;i<raw.length;i+=2) words[i>>1]=raw.readUInt16LE(i);
    // Tests start at a supplied function entry. Reserve reset-vector words
    // exclusively for the modeled ISR, outside all compared control flow.
    words.set(irqCode,0);
    const cpu=new CPU(words,0x4000);
    return {cpu,mem:Buffer.from(cpu.data.buffer),raw};
}
const original=machine(rawPath), candidate=machine(candidatePath);
const patterns=Array.from({length:16},(_,seed)=>{
    const bytes=Buffer.alloc(original.mem.length);
    let v=seed+1;
    for(let i=0;i<bytes.length;i++) {
        v=(Math.imul(v,1664525)+1013904223)>>>0;
        bytes[i]=v>>>24;
    }
    return bytes;
});
const stackTops=[0x0200,0x20ff,0x3000,0x3001,0x3003,0x3008,0x300f,0x40ff];
const hex=n=>'0x'+n.toString(16);
function firstDifference(a,b,start,end,label) {
    if(a.compare(b,start,end,start,end)===0) return;
    for(let i=start;i<end;i++) {
        if(a[i]!==b[i]) throw Error(label+' at '+hex(i)+': '+hex(a[i])+' vs '+hex(b[i]));
    }
}
function prepare(m,at,seed,flags,sp) {
    const {cpu,mem}=m;
    const pattern=patterns[seed&15];
    pattern.copy(mem);
    // Every scratch register, including Z and r0, starts nonzero. Alternate
    // ABI-zero r1 with nonzero to catch hidden assumptions about flags.
    for(let reg=0;reg<32;reg++) mem[reg]=1+((seed*53+reg*19+at*3)%255);
    if(!(seed&1)) mem[1]=0;
    cpu.SP=sp; mem[0x5f]=flags; cpu.pc=at/2; cpu.cycles=0;
    return pattern;
}
function run(m,range,irqAfter) {
    const {cpu}=m; const end=range.end/2;
    let low=cpu.SP, steps=0, interruptCount=0;
    while(cpu.pc!==end && steps<256) {
        if(cpu.pc<irqCode.length || cpu.pc>=cpu.progMem.length)
            throw Error('Invalid branch from '+hex(range.start)+' to '+hex(cpu.pc*2));
        if(irqAfter.includes(steps) && cpu.interruptsEnabled) {
            const resume=cpu.pc;
            avrInterrupt(cpu,0); low=Math.min(low,cpu.SP);
            for(let i=0;i<irqCode.length;i++) {avrInstruction(cpu);low=Math.min(low,cpu.SP);}
            assert.equal(cpu.pc,resume,'ISR did not restore PC');
            interruptCount++;
        }
        avrInstruction(cpu);
        low=Math.min(low,cpu.SP);
        steps++;
    }
    assert.equal(cpu.pc,end,'AVR instruction bound exceeded at '+hex(cpu.pc*2)+' for '+hex(range.start));
    return {low,steps,cycles:cpu.cycles,interruptCount};
}
let comparisons=0, withIRQ=0, irqCalls=0, peakExtra=0, minClocks=Infinity, maxClocks=-Infinity;
for(const range of sites) {
    // Verify the original range really is nothing but the stated register
    // saves. This supplies an independent reference even for unchanged sites.
    const regs=[];
    for(let a=range.start;a<range.end;a+=2) {
        const op=original.raw.readUInt16LE(a);
        assert.equal(op & 0xfe0f,0x920f,'Unexpected OEM instruction at '+hex(a));
        regs.push((op>>4)&31);
    }
    assert(!original.raw.subarray(range.start,range.end).equals(candidate.raw.subarray(range.start,range.end)),
        'Missing replacement at '+hex(range.start));
    for(let flags=0;flags<256;flags++) {
        const seed=flags+range.start/2;
        // Cross a stack page at different points in the original/packed
        // prologues, including the highest legal ATmega1284P stack address.
        const sp=stackTops[seed%stackTops.length];
        for(const interrupted of (flags===0x80||flags===0xff ? [false,true] : [false])) {
            try {
                const before=prepare(original,range.start,seed,flags,sp);
                prepare(candidate,range.start,seed,flags,sp);
                const gprs=Buffer.from(original.mem.subarray(0,32));
                // Interrupt immediately after first helper POP and near its
                // deepest stack frame. The stock sequence has different
                // length; fire both IRQs in its corresponding early/late paths.
                const a=run(original,range,interrupted?[2,regs.length-1]:[]);
                const b=run(candidate,range,interrupted?[4,regs.length+6]:[]);
                assert.equal(a.interruptCount,interrupted?2:0);
                assert.equal(b.interruptCount,a.interruptCount);
                const finalSP=sp-regs.length;
                assert.equal(original.cpu.SP,finalSP);
                assert.equal(candidate.cpu.SP,finalSP);
                firstDifference(original.mem,gprs,0,32,'Stock GPR');
                firstDifference(candidate.mem,gprs,0,32,'Rewritten GPR');
                assert.equal(original.mem[0x5f],flags);
                assert.equal(candidate.mem[0x5f],flags);
                for(let i=0;i<regs.length;i++)
                    assert.equal(original.mem[sp-i],gprs[regs[i]],'Original pushed register differs');
                firstDifference(original.mem,candidate.mem,0,0x100,'GPR or I/O difference');
                firstDifference(original.mem,candidate.mem,finalSP+1,original.mem.length,'Live stack or upper SRAM');
                for(const [m,r] of [[original,a],[candidate,b]]) {
                    firstDifference(m.mem,before,0x100,r.low+1,'SRAM below touched stack');
                    firstDifference(m.mem,before,sp+1,m.mem.length,'SRAM above initial stack');
                }
                comparisons++;
                if(interrupted) {withIRQ++;irqCalls+=b.interruptCount;}
                else {
                    peakExtra=Math.max(peakExtra,a.low-b.low);
                    minClocks=Math.min(minClocks,b.cycles-a.cycles);
                    maxClocks=Math.max(maxClocks,b.cycles-a.cycles);
                }
            } catch(err) {
                throw Error('Prologue '+hex(range.start)+'..'+hex(range.end)+', SREG '+hex(flags)+
                            ', initial SP '+hex(sp)+', interrupts '+interrupted+': '+err.message,{cause:err});
            }
        }
    }
}
console.log(sites.length+' original AVR prologues, '+comparisons+' complete-state comparisons; '+
            withIRQ+' interrupted comparisons ('+irqCalls+' IRQs per image); peak extra stack '+peakExtra+
            ' bytes; extra cycles '+minClocks+'..'+maxClocks);
