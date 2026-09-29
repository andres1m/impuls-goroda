package app

import (
	"errors"
	"fmt"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

func presetScenarioInput(presetID string) (routewire.BotScenarioInput, error) {
	const (
		contemporaryArt uint64 = 1 << 0
		classicalArt    uint64 = 1 << 1
		scienceTech     uint64 = 1 << 2
		streetWorkout   uint64 = 1 << 3
		runningPark     uint64 = 1 << 4
		ecoVolunteer    uint64 = 1 << 5
		socialVolunteer uint64 = 1 << 6
		gastroCoffee    uint64 = 1 << 7
		performingArts  uint64 = 1 << 8
		excursions      uint64 = 1 << 9
		cityWalk        uint64 = 1 << 10
		workshops       uint64 = 1 << 11
		cinema          uint64 = 1 << 12
	)
	var mask uint64
	profile := "moderate"
	switch presetID {
	case "vibe":
		mask = contemporaryArt | gastroCoffee | cityWalk | cinema
	case "mood":
		mask = runningPark | gastroCoffee | cityWalk
		profile = "relaxed"
	case "culture":
		mask = classicalArt | performingArts | excursions
	case "energy":
		mask = streetWorkout | runningPark | cityWalk
		profile = "intense"
	case "balance":
		mask = contemporaryArt | gastroCoffee | cityWalk
	case "benefit":
		mask = scienceTech | ecoVolunteer | socialVolunteer | workshops
	default:
		return routewire.BotScenarioInput{}, errors.New("unknown scenario preset")
	}
	encodedMask := fmt.Sprintf("0x%016x", mask)
	return routewire.BotScenarioInput{Constraints: &routewire.BotScenarioConstraints{
		InterestMask: &encodedMask, LoadProfile: &profile,
	}}, nil
}
