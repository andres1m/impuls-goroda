package domain

import "errors"

// Candidate is one way to visit a place: an event session, or the place itself during opening hours.
type Candidate struct {
	Place     Place
	Event     *Event
	Session   *Session
	Window    VisitWindow
	Entrances []Entrance
	Offers    []PriceOffer
	BaseScore float64
}

func (c Candidate) Validate() error {
	if err := c.Place.Validate(); err != nil {
		return err
	}
	if (c.Event == nil) != (c.Session == nil) {
		return errors.New("candidate event and session must be given together")
	}
	if c.Event == nil {
		if c.Place.Category == nil {
			return errors.New("place visit without an event requires a place category")
		}
		if len(c.Offers) > 0 {
			return errors.New("price offers require a session")
		}
	} else {
		if err := c.Event.Validate(); err != nil {
			return err
		}
		if err := c.Session.Validate(); err != nil {
			return err
		}
		if c.Event.PlaceID != c.Place.ID || c.Session.EventID != c.Event.ID {
			return errors.New("candidate place, event and session do not belong together")
		}
	}
	if err := c.Window.Validate(); err != nil {
		return err
	}
	for _, entrance := range c.Entrances {
		if err := entrance.Validate(); err != nil {
			return err
		}
		if entrance.PlaceID != c.Place.ID {
			return errors.New("candidate entrance belongs to another place")
		}
	}
	for _, offer := range c.Offers {
		if err := offer.Validate(); err != nil {
			return err
		}
		if offer.SessionID != c.Session.ID {
			return errors.New("candidate price offer belongs to another session")
		}
	}
	if !finite(c.BaseScore) || c.BaseScore < 0 {
		return errors.New("candidate base score must be finite and non-negative")
	}
	return nil
}

// Category is the event's category, or the place's own one for a visit without an event.
func (c Candidate) Category() Category {
	if c.Event != nil {
		return c.Event.Category
	}
	if c.Place.Category != nil {
		return *c.Place.Category
	}
	return ""
}

// InterestMask follows the same rule as Category, so a venue's other events do not inflate affinity.
func (c Candidate) InterestMask() InterestMask {
	if c.Event != nil {
		return c.Event.InterestMask
	}
	return c.Place.InterestMask
}

func (c Candidate) DataMode() DataMode {
	mode := c.Place.DataMode
	if c.Event != nil {
		mode = Weakest(mode, c.Event.DataMode)
	}
	if c.Session != nil {
		mode = Weakest(mode, c.Session.DataMode)
	}
	return mode
}
