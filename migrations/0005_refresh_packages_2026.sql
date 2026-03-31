-- Refresh public package lineup:
-- - Photo: Starter / Basic
-- - Video: Starter / Basic
-- - Combo: S / M
-- Prices are set in RUB so larger bundles are cheaper per unit.

UPDATE packages
SET is_active = FALSE,
    updated_at = now()
WHERE code IN (
  'img_30', 'img_100', 'img_300',
  'vid_5', 'vid_15', 'vid_40',
  'mix_30_5', 'mix_100_15', 'mix_300_40',
  'starter', 'media', 'protxt',
  'photo_starter', 'photo_basic',
  'video_starter', 'video_basic',
  'combo_s', 'combo_m'
);

INSERT INTO packages(code, title, price_rub, currency, text_credits, image_credits, video_credits, is_active)
VALUES
  ('photo_starter', 'Starter',                 149, 'RUB', 0, 10,  0, TRUE),
  ('photo_basic',   'Basic',                   279, 'RUB', 0, 20,  0, TRUE),

  ('video_starter', 'Starter',                 249, 'RUB', 0, 0,   1, TRUE),
  ('video_basic',   'Basic',                  1090, 'RUB', 0, 0,   5, TRUE),

  ('combo_s',       'Combo S',                1590, 'RUB', 0, 50,  5, TRUE),
  ('combo_m',       'Combo M (Most Popular)', 3890, 'RUB', 0, 120, 15, TRUE)
ON CONFLICT (code) DO UPDATE
SET title = EXCLUDED.title,
    price_rub = EXCLUDED.price_rub,
    currency = EXCLUDED.currency,
    text_credits = EXCLUDED.text_credits,
    image_credits = EXCLUDED.image_credits,
    video_credits = EXCLUDED.video_credits,
    is_active = EXCLUDED.is_active,
    updated_at = now();
