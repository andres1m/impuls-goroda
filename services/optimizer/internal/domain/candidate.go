package domain

import "errors"

// Candidate is one way to visit a place: an event session, or the place itself during opening hours.
type Candidate struct {
	Place     Place        `json:"Place"`
	Event     *Event       `json:"Event"`
	Session   *Session     `json:"Session"`
	Window    VisitWindow  `json:"Window"`
	Entrances []Entrance   `json:"Entrances"`
	Offers    []PriceOffer `json:"Offers"`
	BaseScore float64      `json:"BaseScore"`
}

func (c *Candidate) Validate() error {
	if err := c.Place.Validate(); err != nil {
		return err
	}
	if err := c.validateEventSession(); err != nil {
		return err
	}
	if err := c.Window.Validate(); err != nil {
		return err
	}
	if err := c.validateEntrancesAndOffers(); err != nil {
		return err
	}
	if !finite(c.BaseScore) || c.BaseScore < 0 {
		return errors.New("candidate base score must be finite and non-negative")
	}
	return nil
}

func (c *Candidate) validateEventSession() error {
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
		return nil
	}
	if err := c.Event.Validate(); err != nil {
		return err
	}
	if err := c.Session.Validate(); err != nil {
		return err
	}
	if c.Event.PlaceID != c.Place.ID || c.Session.EventID != c.Event.ID {
		return errors.New("candidate place, event and session do not belong together")
	}
	return nil
}

func (c *Candidate) validateEntrancesAndOffers() error {
	for i := range c.Entrances {
		entrance := &c.Entrances[i]
		if err := entrance.Validate(); err != nil {
			return err
		}
		if entrance.PlaceID != c.Place.ID {
			return errors.New("candidate entrance belongs to another place")
		}
	}
	for i := range c.Offers {
		offer := &c.Offers[i]
		if err := offer.Validate(); err != nil {
			return err
		}
		if offer.SessionID != c.Session.ID {
			return errors.New("candidate price offer belongs to another session")
		}
	}
	return nil
}

// Category is the event's category, or the place's own one for a visit without an event.
func (c *Candidate) Category() Category {
	if c.Event != nil {
		return c.Event.Category
	}
	if c.Place.Category != nil {
		return *c.Place.Category
	}
	return ""
}

// InterestMask follows the same rule as Category, so a venue's other events do not inflate affinity.
func (c *Candidate) InterestMask() InterestMask {
	if c.Event != nil {
		return c.Event.InterestMask
	}
	return c.Place.InterestMask
}

func (c *Candidate) DataMode() DataMode {
	mode := c.Place.DataMode
	if c.Event != nil {
		mode = Weakest(mode, c.Event.DataMode)
	}
	if c.Session != nil {
		mode = Weakest(mode, c.Session.DataMode)
	}
	return mode
}
