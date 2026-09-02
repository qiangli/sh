#include "textflag.h"

// func libcSigaction(sig uint32, new, old *signalDisposition) int32
TEXT ·libcSigaction(SB),NOSPLIT,$0-32
	MOVL	sig+0(FP), DI
	MOVQ	new+8(FP), SI
	MOVQ	old+16(FP), DX
	CALL	libc_sigaction(SB)
	MOVL	AX, ret+24(FP)
	RET
