package feed

import (
	"context"
	"deck2/internal/events"

	"deck2/internal/registry"
)

type secureAddressView struct {
	HTTPS string `json:"https,omitempty"`
}

// secureAddress is the https address of deck on the owner's tailnet once deck is on it.
func (s *Service) secureAddress(ctx context.Context, act registry.Actor, _ struct{}) (secureAddressView, error) {
	if a := s.Tailnet.Load(); a != nil {
		return secureAddressView{HTTPS: *a}, nil
	}
	return secureAddressView{}, nil
}

func (s *Service) voiceCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("secure_address_get", "Secure address",
			"Gives the https address of deck on the owner's tailnet, where a browser lets a page record from the microphone; none while deck is not on one.",
			registry.OwnerOnly, s.secureAddress).On(events.Health),
	}
}
