BEGIN;

DELETE FROM ref.interest_tag WHERE code IN (
    'contemporary_art', 'classical_art', 'science_tech', 'street_workout', 'running_park',
    'eco_volunteer', 'social_volunteer', 'gastro_coffee', 'performing_arts', 'excursions',
    'city_walk', 'lectures_workshops', 'cinema'
);

DELETE FROM ref.category WHERE code IN (
    'culture', 'sport', 'volunteer', 'walk', 'tourism', 'gastro'
);

COMMIT;
