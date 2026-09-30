-- +goose Up
INSERT INTO domains (id, slug, name) VALUES
  (1,  'manufacturing',         'Manufacturing'),
  (2,  'healthcare',            'Healthcare'),
  (3,  'logistics',             'Logistics'),
  (4,  'retail',                'Retail'),
  (5,  'professional_services', 'Professional services'),
  (6,  'hospitality',           'Hospitality'),
  (7,  'construction',          'Construction'),
  (8,  'education',             'Education'),
  (9,  'nonprofit',             'Nonprofit'),
  (10, 'other',                 'Other');

-- +goose Down
DELETE FROM domains WHERE id BETWEEN 1 AND 10;
