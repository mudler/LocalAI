// SPDX-License-Identifier: MIT
package main

import pb "github.com/mudler/LocalAI/pkg/grpc/proto"

// Status exposes only metadata derived from the loaded encoder. The native
// identity is borrowed, so copy it while holding the context lifetime lock.
func (p *ParakeetCpp) Status() (pb.StatusResponse, error) {
	result, err := p.Base.Status()
	if err != nil {
		return result, err
	}
	p.engineMu.Lock()
	defer p.engineMu.Unlock()
	if p.spkCtx != 0 && CppSpeakerIdentity != nil && CppSpeakerDim != nil {
		identity := CppSpeakerIdentity(p.spkCtx)
		dim := CppSpeakerDim(p.spkCtx)
		if identity != 0 && dim > 0 {
			result.SpeakerEncoder = &pb.SpeakerEncoder{Identity: goStringFromCPtr(identity), Dimension: dim}
		}
	}
	return result, nil
}
