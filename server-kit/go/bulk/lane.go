package bulk

import "strings"

func (p *Pipeline) PlanLane(req LaneRequest) LanePlan {
	if req.Capabilities.OS == "" {
		req.Capabilities = DetectPlatformCapabilities()
	}
	candidates := []LaneCandidate{
		descriptorCandidate(req),
		signedObjectStoreCandidate(req),
		kernelZeroCopyCandidate(req),
		mptcpCandidate(req),
		quicCandidate(req),
		httpStreamCandidate(req),
	}
	selected := LaneHTTPStream
	for _, candidate := range candidates {
		if candidate.Available {
			selected = candidate.Kind
			break
		}
	}
	diagnostic := p.Diagnostics(req.Plan)
	diagnostic.Ingress = string(selected)
	diagnostic.ZeroCopyAvailable = req.Capabilities.ZeroCopyAvailable
	diagnostic.MPTCPAvailable = req.Capabilities.MPTCPAvailable
	diagnostic.QUICAvailable = req.QUICAdapterAvailable
	diagnostic.KernelPacing = req.Capabilities.KernelPacing
	diagnostic.Fallback = fallbackSummary(selected, candidates)
	attrs := cloneOptionalStringMap(diagnostic.Attributes)
	if attrs == nil {
		attrs = make(map[string]string, 6)
	}
	attrs["selected_lane"] = string(selected)
	for _, candidate := range candidates {
		if !candidate.Available && candidate.Reason != "" {
			attrs["lane_"+string(candidate.Kind)] = candidate.Reason
		}
	}
	diagnostic.Attributes = attrs
	return LanePlan{
		Selected:   selected,
		Candidates: candidates,
		Diagnostic: diagnostic,
	}
}

func descriptorCandidate(req LaneRequest) LaneCandidate {
	if req.Locality != "same-host" {
		return LaneCandidate{Kind: LaneDescriptor, Reason: "producer is not same-host"}
	}
	if !req.TrustedProducer {
		return LaneCandidate{Kind: LaneDescriptor, Reason: "producer is not trusted for descriptor access"}
	}
	if !req.DescriptorAvailable {
		return LaneCandidate{Kind: LaneDescriptor, Reason: "descriptor source is unavailable"}
	}
	return LaneCandidate{Kind: LaneDescriptor, Available: true}
}

func signedObjectStoreCandidate(req LaneRequest) LaneCandidate {
	if !req.DirectObjectStore {
		return LaneCandidate{Kind: LaneSignedObjectStore, Reason: "direct object-store upload is unavailable"}
	}
	if req.Plan.Compression != EncodingIdentity {
		return LaneCandidate{Kind: LaneSignedObjectStore, Reason: "signed verification currently requires identity encoding"}
	}
	return LaneCandidate{Kind: LaneSignedObjectStore, Available: true}
}

func kernelZeroCopyCandidate(req LaneRequest) LaneCandidate {
	if !req.KernelAdapterEnabled {
		return LaneCandidate{Kind: LaneKernelZeroCopy, Reason: "kernel adapter is not enabled"}
	}
	if !req.Capabilities.ZeroCopyAvailable {
		return LaneCandidate{Kind: LaneKernelZeroCopy, Reason: "platform zero-copy capability not detected"}
	}
	if req.Locality == "internet" && req.DirectObjectStore {
		return LaneCandidate{Kind: LaneKernelZeroCopy, Reason: "direct object-store upload avoids app-server byte proxy"}
	}
	return LaneCandidate{Kind: LaneKernelZeroCopy, Available: true}
}

func mptcpCandidate(req LaneRequest) LaneCandidate {
	if !req.KernelAdapterEnabled {
		return LaneCandidate{Kind: LaneMPTCP, Reason: "kernel adapter is not enabled"}
	}
	if !req.Capabilities.MPTCPAvailable {
		return LaneCandidate{Kind: LaneMPTCP, Reason: "MPTCP capability not detected"}
	}
	return LaneCandidate{Kind: LaneMPTCP, Available: true}
}

func quicCandidate(req LaneRequest) LaneCandidate {
	if !req.QUICAdapterAvailable {
		return LaneCandidate{Kind: LaneQUIC, Reason: "QUIC adapter is unavailable"}
	}
	return LaneCandidate{Kind: LaneQUIC, Available: true}
}

func httpStreamCandidate(req LaneRequest) LaneCandidate {
	if !req.HTTPStreamAvailable {
		return LaneCandidate{Kind: LaneHTTPStream, Reason: "HTTP stream reader is unavailable"}
	}
	return LaneCandidate{Kind: LaneHTTPStream, Available: true}
}

func fallbackSummary(selected LaneKind, candidates []LaneCandidate) string {
	if selected == "" {
		return "no lane selected"
	}
	var unavailable []string
	for _, candidate := range candidates {
		if candidate.Kind == selected {
			break
		}
		if !candidate.Available && candidate.Reason != "" {
			unavailable = append(unavailable, string(candidate.Kind)+": "+candidate.Reason)
		}
	}
	if len(unavailable) == 0 {
		return ""
	}
	return strings.Join(unavailable, "; ")
}
