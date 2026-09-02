#include "textflag.h"

// func libcSigactionAddr() uintptr
TEXT ·libcSigactionAddr(SB),NOSPLIT,$0-8
	MOVQ	$libc_sigaction(SB), AX
	MOVQ	AX, ret+0(FP)
	RET
