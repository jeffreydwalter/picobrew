.section .key_outline,"ax",@progbits
.global key_outline
key_outline:
    ldi r20, 0
    rjmp key_box

.section .key_fill,"ax",@progbits
.global key_fill
key_fill:
    ldi r20, 1
    rjmp key_box
