package llm

func ParseCapability(value string) (Capability, bool) {
	switch value {
	case CapChat.String():
		return CapChat, true
	case CapToolCall.String():
		return CapToolCall, true
	case CapStructuredOutput.String():
		return CapStructuredOutput, true
	case CapStream.String():
		return CapStream, true
	case CapEmbed.String():
		return CapEmbed, true
	default:
		return 0, false
	}
}

func CapabilitiesFromStrings(values []string) []Capability {
	out := make([]Capability, 0, len(values))
	for _, value := range values {
		capability, ok := ParseCapability(value)
		if ok {
			out = append(out, capability)
		}
	}
	return out
}

func ProfileSupports(profile Profile, capability Capability, defaults ...Capability) bool {
	if len(profile.Capabilities) > 0 {
		for _, candidate := range profile.Capabilities {
			if candidate == capability {
				return true
			}
		}
		return false
	}
	for _, candidate := range defaults {
		if candidate == capability {
			return true
		}
	}
	return false
}
