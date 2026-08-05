// Package server exposes the Runtime Brain over gRPC.
//
// This file is the only place in the core that knows both the wire types and
// the domain types. Keeping the translation here is what lets the engines stay
// ignorant of transport entirely (docs/ARCHITECTURE.md §4).
package server

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/airuntimeguard/core/domain"
	pb "github.com/airuntimeguard/core/gen/runtime/v1"
)

func toDomainSignal(p *pb.Signal) domain.Signal {
	if p == nil {
		return domain.Signal{}
	}

	s := domain.Signal{
		ID:        p.GetId(),
		SessionID: p.GetSessionId(),
		Agent:     p.GetAgent(),
		Seq:       p.GetSeq(),
		Phase:     domain.Phase(p.GetPhase()),
		Kind:      domain.Kind(p.GetKind()),
		RawRef:    p.GetRawRef(),
	}

	if t := p.GetObservedAt(); t != nil {
		s.ObservedAt = t.AsTime()
	}
	if a := p.GetActor(); a != nil {
		s.Actor = domain.Actor{Type: domain.ActorType(a.GetType()), Name: a.GetName()}
	}
	if t := p.GetTarget(); t != nil {
		s.Target = domain.Target{
			Type:  domain.TargetType(t.GetType()),
			Value: t.GetValue(),
			Scope: domain.Scope(t.GetScope()),
		}
	}

	if attrs := p.GetAttributes(); attrs != nil {
		s.Attributes = attrs.AsMap()
		// secret_shape and secret_count ride in attributes on the wire so the
		// proto stays open to new shapes without a schema bump. They are lifted
		// into typed fields here because every engine reads them.
		if v, ok := s.Attributes["secret_shape"].(string); ok {
			s.SecretShape = domain.SecretShape(v)
		}
		if v, ok := s.Attributes["secret_count"].(float64); ok {
			s.SecretCount = int(v)
		}
		if v, ok := s.Attributes["supervision"].(float64); ok {
			s.Supervision = domain.Supervision(v)
		}
		if v, ok := s.Attributes["transfer"].(float64); ok {
			s.Transfer = domain.Transfer(v)
		}
	}

	return s
}

func fromDomainDecision(d domain.Decision) *pb.Decision {
	out := &pb.Decision{
		Id:          d.ID,
		SessionId:   d.SessionID,
		SignalId:    d.SignalID,
		Action:      pb.Action(d.Action),
		Risk:        fromDomainRisk(d.Risk),
		Policies:    d.Policies,
		Explanation: fromDomainExplanation(d.Explanation),
		DecidedAt:   timestamppb.New(d.DecidedAt),
		LatencyUs:   uint32(d.Latency.Microseconds()),
	}

	if i := d.Interaction; i != nil {
		options := make([]*pb.Option, 0, len(i.Options))
		for _, o := range i.Options {
			options = append(options, &pb.Option{
				Id:     o.ID,
				Label:  o.Label,
				Effect: pb.Action(o.Effect),
				Learns: o.Learns,
			})
		}
		out.Interaction = &pb.Interaction{
			PromptId:        i.PromptID,
			ChannelHint:     pb.ChannelHint(i.ChannelHint),
			HeadlessDefault: pb.Action(i.HeadlessDefault),
			TimeoutMs:       uint32(i.Timeout.Milliseconds()),
			Options:         options,
		}
	}

	return out
}

func fromDomainExplanation(e domain.Explanation) *pb.Explanation {
	return &pb.Explanation{
		Summary:  e.Summary,
		What:     e.What,
		Why:      e.Why,
		Evidence: e.Evidence,
		Risk: &pb.RiskSummary{
			Score:      int32(e.Risk.Score),
			Band:       pb.SafetyState(e.Risk.Band()),
			TopFactors: fromDomainFactors(e.Risk.TopFactors(3)),
		},
		Guidance: e.Guidance,
	}
}

func fromDomainRisk(r domain.Risk) *pb.Risk {
	return &pb.Risk{
		Score:         int32(r.Score),
		Confidence:    float32(r.Confidence),
		Factors:       fromDomainFactors(r.Factors),
		ComputedAt:    timestamppb.New(r.ComputedAt),
		ConfigVersion: r.ConfigVersion,
	}
}

func fromDomainFactors(in []domain.Factor) []*pb.Factor {
	out := make([]*pb.Factor, 0, len(in))
	for _, f := range in {
		out = append(out, &pb.Factor{
			Name:         f.Name,
			Contribution: int32(f.Contribution),
			Evidence:     f.Evidence,
			Description:  f.Description,
		})
	}
	return out
}

func fromDomainSession(s *domain.Session) *pb.Session {
	if s == nil {
		return nil
	}

	caps := &pb.Capabilities{}
	set := map[domain.CapabilityName]**pb.Capability{
		domain.CapSecretAccess:       &caps.SecretAccess,
		domain.CapFilesystemRead:     &caps.FilesystemRead,
		domain.CapFilesystemWrite:    &caps.FilesystemWrite,
		domain.CapShellExec:          &caps.ShellExec,
		domain.CapOutboundNetwork:    &caps.OutboundNetwork,
		domain.CapGitWrite:           &caps.GitWrite,
		domain.CapUntrustedContext:   &caps.UntrustedContext,
		domain.CapCredentialMaterial: &caps.CredentialMaterial,
		domain.CapDataEgress:         &caps.DataEgress,
	}
	for name, field := range set {
		entry, ok := s.Capabilities[name]
		if !ok {
			continue
		}
		*field = &pb.Capability{
			Active:    entry.Active,
			FirstSeen: timestamppb.New(entry.FirstSeen),
			Count:     uint32(entry.Count),
			Evidence:  entry.Evidence,
		}
	}

	return &pb.Session{
		Id:           s.ID,
		Agent:        s.Agent,
		StartedAt:    timestamppb.New(s.StartedAt),
		LastSignalAt: timestamppb.New(s.LastSignalAt),
		State:        pb.SafetyState(s.State),
		Capabilities: caps,
		Risk:         fromDomainRisk(s.Risk),
		SignalCount:  s.SignalCount,
	}
}

func toDomainResolution(p *pb.ResolveRequest, now time.Time) domain.Resolution {
	return domain.Resolution{
		PromptID: p.GetPromptId(),
		OptionID: p.GetOptionId(),
		Source:   domain.ResolutionSource(p.GetSource()),
		Channel:  p.GetChannel(),
		At:       now,
	}
}
