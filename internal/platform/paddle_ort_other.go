//go:build !(windows && amd64) && !((darwin || linux) && (amd64 || arm64))

package platform

import "os/exec"

// 이 운영체제·CPU 에서는 PaddleOCR 을 쓰지 않는다 (Windows 글자 인식만).
func ortCall(fn uintptr, args ...uintptr) uintptr { return 0 }

func ortCandidates() []ortCandidate { return nil }

func paddleWorkerCmd(cmd *exec.Cmd) *exec.Cmd { return cmd }
