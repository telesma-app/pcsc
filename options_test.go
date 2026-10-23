package pcsc

import "testing"

func TestOpenOptions(t *testing.T) {
	options := newOpenOptions(
		WithShareMode(ShareModeExclusive),
		WithPreferredProtocols(ProtocolT1),
		WithDisconnectDisposition(DispositionResetCard),
	)

	if options.shareMode != ShareModeExclusive {
		t.Fatalf("share mode = %d, want %d", options.shareMode, ShareModeExclusive)
	}

	if options.preferredProtocols != ProtocolT1 {
		t.Fatalf("preferred protocols = %d, want %d", options.preferredProtocols, ProtocolT1)
	}

	if options.disconnectDisposition != DispositionResetCard {
		t.Fatalf("disconnect disposition = %d, want %d", options.disconnectDisposition, DispositionResetCard)
	}
}

func TestDirectOpenDefaultsToUndefinedProtocol(t *testing.T) {
	options := newOpenOptions(WithShareMode(ShareModeDirect))

	if options.preferredProtocols != ProtocolUndefined {
		t.Fatalf("preferred protocols = %d, want undefined", options.preferredProtocols)
	}
}
