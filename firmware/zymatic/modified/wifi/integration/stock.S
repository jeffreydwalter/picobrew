/* Independent foreground stubs for the verified 1.1.14 image. GNU AVR
 * assembly spells absolute CALL/JMP destinations as byte addresses. */
.section .text.zym_wifi_client,"ax",@progbits
.global zym_wifi_client
zym_wifi_client:
    lds r24, zym_wifi_flags
    sbrc r24,0
    rjmp 1f
    lds r24, 0x16bc
    lds r25, 0x16bd
    ret
1:  ldi r24,0
    ldi r25,0
    ret

.macro guarded name, address
.section .text.\name,"ax",@progbits
.global \name
\name:
    lds r0, zym_wifi_flags
    sbrs r0,0
    jmp \address
    ldi r24,0
    ret
.endm

/* The two constructors may allocate and then start the synchronous OEM
 * CC3000. Their call sites explicitly branch on a zero return. */
guarded zym_wifi_client_new, 0x3ae0
guarded zym_wifi_client_setup, 0x8880
/* 0xb6b2 drives interactive network configuration, including a path that
 * ignores the constructor result. Its two callers accept cancellation. */
guarded zym_wifi_config, 0xb6b2

.section .text.zym_wifi_stop,"ax",@progbits
.global zym_wifi_stop
zym_wifi_stop:
    call zym_wifi_before_restart
    jmp 0x15388

.section .text.zym_wifi_start,"ax",@progbits
.global zym_wifi_start
zym_wifi_start:
    push r24
    push r25
    call zym_wifi_before_restart
    pop r25
    pop r24
    jmp 0x153c2
