/* Preserve r8, then pass the original eight output pointers unchanged and
 * the selected transport in r8 (ninth AVR-GCC argument). */
.section .wifi,"ax",@progbits
.global wifi
wifi:
 push r8
 clr r8
 inc r8
 rjmp common
.section .ethernet,"ax",@progbits
.global ethernet
ethernet:
 push r8
 clr r8
 jmp common
.section .wifi,"ax",@progbits
common:
 call parse
 pop r8
 ret
