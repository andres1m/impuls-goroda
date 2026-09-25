BEGIN;

INSERT INTO ref.category (code, title) VALUES
    ('culture', 'Культура'),
    ('sport', 'Спорт'),
    ('volunteer', 'Волонтёрство'),
    ('walk', 'Прогулки'),
    ('tourism', 'Туризм'),
    ('gastro', 'Гастрономия');

-- A published bit position is never reused: stored masks would silently change meaning.
INSERT INTO ref.interest_tag (bit, code, title) VALUES
    (0, 'contemporary_art', 'Современное искусство'),
    (1, 'classical_art', 'Классические музеи и история'),
    (2, 'science_tech', 'Наука и технологии'),
    (3, 'street_workout', 'Уличный спорт'),
    (4, 'running_park', 'Бег и набережные'),
    (5, 'eco_volunteer', 'Экологическое волонтёрство'),
    (6, 'social_volunteer', 'Социальное волонтёрство'),
    (7, 'gastro_coffee', 'Кофейни и локальная гастрономия'),
    (8, 'performing_arts', 'Театр и концерты'),
    (9, 'excursions', 'Экскурсии и достопримечательности'),
    (10, 'city_walk', 'Прогулки по городу и паркам'),
    (11, 'lectures_workshops', 'Лекции и мастер-классы'),
    (12, 'cinema', 'Кино');

COMMIT;
