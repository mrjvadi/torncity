package content

import (
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/payment"
)

// This file holds which payment methods each service accepts
// (configs/content/payments.yml): cash, card, or both.
//
// HOW a charge is paid is one rule, in internal/domain/payment and
// application.Pay. WHICH methods a service takes is content: a university and
// a bus company take cards; a street vendor or the black market may take cash
// only. A service with no entry here takes both. A course (education.yml) or
// a transport mode (transport.yml) may narrow its service further with its
// own `payment:` list.

// Payment services: every place in the code that charges a player names one.
// The set is closed because each member is a charge site; content decides
// only what each one accepts.
const (
	ServiceTuition     payment.Service = "tuition"
	ServiceFare        payment.Service = "fare"
	ServiceCrimeReport payment.Service = "crime_report"
	ServiceBail        payment.Service = "bail"
	ServiceShop        payment.Service = "shop"
	ServiceMarket      payment.Service = "market"
	ServiceAuction     payment.Service = "auction"
	ServiceElection    payment.Service = "election"
	// ServiceCompany is a company's registration fee, and money its owner or
	// manager puts into it.
	ServiceCompany payment.Service = "company"
)

// knownServices is the closed set content may name.
var knownServices = map[payment.Service]bool{
	ServiceTuition: true, ServiceFare: true, ServiceCrimeReport: true, ServiceBail: true,
	ServiceShop: true, ServiceMarket: true, ServiceAuction: true, ServiceElection: true,
	ServiceCompany: true,
}

// PaymentServiceDef is one entry of payments.yml.
type PaymentServiceDef struct {
	// Code is one of the payment services above.
	Code string `yaml:"code" json:"code"`
	// Accepts lists the methods it takes: cash, card. It may not be empty:
	// leave the service out to take both.
	Accepts []string `yaml:"accepts" json:"accepts"`
}

// Validation failures for payment content.
var (
	// ErrUnknownPaymentService means payments.yml names a service no code
	// charges for.
	ErrUnknownPaymentService = errors.New("content: unknown payment service")
	// ErrDuplicatePaymentService means one service is listed twice.
	ErrDuplicatePaymentService = errors.New("content: duplicate payment service")
	// ErrInvalidPaymentMethods means an accepts list is empty or names an
	// unknown or repeated method.
	ErrInvalidPaymentMethods = errors.New("content: invalid payment methods")
)

// validatePayments checks payments.yml and every per-entry payment list.
func (p *Pack) validatePayments(problems *[]error) {
	add := func(err error) { *problems = append(*problems, err) }
	seen := map[string]bool{}
	for i, s := range p.PaymentServices {
		switch {
		case !knownServices[payment.Service(s.Code)]:
			add(fmt.Errorf("%w: payment_services[%d] %q", ErrUnknownPaymentService, i, s.Code))
			continue
		case seen[s.Code]:
			add(fmt.Errorf("%w: %q", ErrDuplicatePaymentService, s.Code))
			continue
		}
		seen[s.Code] = true
		if len(s.Accepts) == 0 {
			add(fmt.Errorf("%w: service %q accepts nothing", ErrInvalidPaymentMethods, s.Code))
			continue
		}
		if _, err := payment.ParseAccepts(s.Accepts); err != nil {
			add(fmt.Errorf("%w: service %q: %w", ErrInvalidPaymentMethods, s.Code, err))
		}
	}
	service := map[payment.Service]payment.Accepts{}
	for _, s := range p.PaymentServices {
		service[payment.Service(s.Code)] = accepts(s.Accepts)
	}
	check := func(svc payment.Service, what, code string, raw []string) {
		if raw == nil {
			return
		}
		if len(raw) == 0 {
			add(fmt.Errorf("%w: %s %q accepts nothing", ErrInvalidPaymentMethods, what, code))
			return
		}
		own, err := payment.ParseAccepts(raw)
		if err != nil {
			add(fmt.Errorf("%w: %s %q: %w", ErrInvalidPaymentMethods, what, code, err))
			return
		}
		// A narrowing that leaves nothing the service takes would be a
		// service nobody can ever pay.
		for _, m := range own {
			if service[svc].Has(m) {
				return
			}
		}
		add(fmt.Errorf("%w: %s %q accepts %v, none of which its service %q takes",
			ErrInvalidPaymentMethods, what, code, raw, svc))
	}
	for _, c := range p.Courses {
		check(ServiceTuition, "course", c.Code, c.Payment)
	}
	for _, m := range p.TransportModes {
		check(ServiceFare, "transport mode", m.Code, m.Payment)
	}
}

// accepts parses an authored list that has passed validation.
func accepts(raw []string) payment.Accepts {
	a, err := payment.ParseAccepts(raw)
	if err != nil {
		return nil
	}
	return a
}

// Accepts returns the methods a service takes: its payments.yml entry, or
// every method when it has none.
func (s *Snapshot) Accepts(service payment.Service) payment.Accepts {
	if a, ok := s.payments[service]; ok {
		return a.Normalize()
	}
	return payment.Accepts(nil).Normalize()
}

// narrow is the methods a service takes, narrowed by an entry's own list.
func (s *Snapshot) narrow(service payment.Service, own []string) payment.Accepts {
	base := s.Accepts(service)
	if own == nil {
		return base
	}
	var out payment.Accepts
	for _, m := range accepts(own).Normalize() {
		if base.Has(m) {
			out = append(out, m)
		}
	}
	return out
}

// CourseAccepts returns the methods a course's tuition may be paid by.
func (s *Snapshot) CourseAccepts(code string) payment.Accepts {
	def, _ := s.CourseDef(code)
	return s.narrow(ServiceTuition, def.Payment)
}

// ModeAccepts returns the methods a transport mode's fare may be paid by.
func (s *Snapshot) ModeAccepts(code string) payment.Accepts {
	for _, n := range s.transport {
		if n.def.Code == code {
			return s.narrow(ServiceFare, n.def.Payment)
		}
	}
	return s.Accepts(ServiceFare)
}

// buildPayments indexes payments.yml. The pack has been validated.
func (s *Snapshot) buildPayments(p *Pack) {
	s.payments = make(map[payment.Service]payment.Accepts, len(p.PaymentServices))
	for _, def := range p.PaymentServices {
		s.payments[payment.Service(def.Code)] = accepts(def.Accepts)
	}
}
