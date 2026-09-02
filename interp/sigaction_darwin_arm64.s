#include "textflag.h"

// func libcSigactionAddr() uintptr
TEXT ·libcSigactionAddr(SB),NOSPLIT,$0-8
	MOVD	$libc_sigaction(SB), R0
	MOVD	R0, ret+0(FP)
	RET
