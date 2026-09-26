package domain

import (
	"errors"
	"math/bits"
)

type Category string

const (
	CategoryCulture   Category = "culture"
	CategorySport     Category = "sport"
	CategoryVolunteer Category = "volunteer"
	CategoryWalk      Category = "walk"
	CategoryTourism   Category = "tourism"
	CategoryGastro    Category = "gastro"
)

const CategoryCount = 6

// Index is the category's slot in per-branch counters, or -1 for an unknown category.
func (c Category) Index() int {
	switch c {
	case CategoryCulture:
		return 0
	case CategorySport:
		return 1
	case CategoryVolunteer:
		return 2
	case CategoryWalk:
		return 3
	case CategoryTourism:
		return 4
	case CategoryGastro:
		return 5
	default:
		return -1
	}
}

func (c Category) Validate() error {
	if c.Index() < 0 {
		return errors.New("invalid category")
	}
	return nil
}

// Published bit positions are never reused, otherwise stored masks would change meaning.
const (
	InterestContemporaryArt = iota
	InterestClassicalArt
	InterestScienceTech
	InterestStreetWorkout
	InterestRunningPark
	InterestEcoVolunteer
	InterestSocialVolunteer
	InterestGastroCoffee
	InterestPerformingArts
	InterestExcursions
	InterestCityWalk
	InterestLecturesWorkshops
	InterestCinema
)

type InterestMask uint64

func Interests(positions ...int) InterestMask {
	var m InterestMask
	for _, p := range positions {
		m |= 1 << p
	}
	return m
}

func (m InterestMask) Matches(other InterestMask) int {
	return bits.OnesCount64(uint64(m & other))
}

func (m InterestMask) IsEmpty() bool {
	return m == 0
}

type Archetype string

const (
	ArchetypeUrbanAvantgarde Archetype = "urban_avantgarde"
	ArchetypeHistoryHeritage Archetype = "history_heritage"
	ArchetypeActionSocial    Archetype = "action_social"
)

func (a Archetype) Mask() InterestMask {
	switch a {
	case ArchetypeUrbanAvantgarde:
		return Interests(InterestContemporaryArt, InterestStreetWorkout, InterestGastroCoffee, InterestCinema)
	case ArchetypeHistoryHeritage:
		return Interests(InterestClassicalArt, InterestPerformingArts, InterestExcursions)
	case ArchetypeActionSocial:
		return Interests(InterestScienceTech, InterestRunningPark, InterestEcoVolunteer, InterestSocialVolunteer, InterestCityWalk, InterestLecturesWorkshops)
	default:
		return 0
	}
}

func (a Archetype) Validate() error {
	if a.Mask() == 0 {
		return errors.New("invalid archetype")
	}
	return nil
}
