#include "textflag.h"

// func libcSigaction(sig uint32, new, old *signalDisposition) int32
TEXT ·libcSigaction(SB),NOSPLIT,$0-32
	MOVW	sig+0(FP), R0
	MOVD	new+8(FP), R1
	MOVD	old+16(FP), R2
	BL	libc_sigaction(SB)
	MOVW	R0, ret+24(FP)
	RET
