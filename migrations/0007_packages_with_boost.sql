-- Refresh public package lineup for 2026 pricing
-- and add one-time boost package.

UPDATE packages
SET is_active = FALSE,
    updated_at = now()
WHERE is_active = TRUE
  AND code IN (
    'photo_starter', 'photo_basic',
    'video_starter', 'video_basic',
    'combo_s', 'combo_m',
    'base_minimum', 'golden_middle', 'luxury_maximum', 'boost_10_2'
  );

INSERT INTO packages(code, title, price_rub, currency, text_credits, image_credits, video_credits, is_active)
VALUES
  ('base_minimum',   'Базовый минимум',    690, 'RUB', 0,  30,  5, TRUE),
  ('golden_middle',  'Золотая середина',  1490, 'RUB', 0, 100, 10, TRUE),
  ('luxury_maximum', 'Роскошный максимум', 3190, 'RUB', 0, 200, 25, TRUE),
  ('boost_10_2',     'Буст',               290, 'RUB', 0,  10,  2, TRUE)
ON CONFLICT (code) DO UPDATE
SET title = EXCLUDED.title,
    price_rub = EXCLUDED.price_rub,
    currency = EXCLUDED.currency,
    text_credits = EXCLUDED.text_credits,
    image_credits = EXCLUDED.image_credits,
    video_credits = EXCLUDED.video_credits,
    is_active = EXCLUDED.is_active,
    updated_at = now();
