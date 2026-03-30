-- Disable text-based purchase packages.
UPDATE packages
SET is_active = FALSE,
    updated_at = now()
WHERE text_credits > 0;

-- New default market-style packages:
-- 1) Photo-only
-- 2) Video-only
-- 3) Combo bundles
-- Larger packages have lower unit price inside each category.
INSERT INTO packages(code, title, price_rub, currency, text_credits, image_credits, video_credits, is_active)
VALUES
  ('img_30',     'Фото S',        249,  'RUB', 0, 30,  0,  TRUE),
  ('img_100',    'Фото M',        699,  'RUB', 0, 100, 0,  TRUE),
  ('img_300',    'Фото L',       1790,  'RUB', 0, 300, 0,  TRUE),

  ('vid_5',      'Видео S',       449,  'RUB', 0, 0,   5,  TRUE),
  ('vid_15',     'Видео M',      1190,  'RUB', 0, 0,   15, TRUE),
  ('vid_40',     'Видео L',      2990,  'RUB', 0, 0,   40, TRUE),

  ('mix_30_5',   'Комбо S',       619,  'RUB', 0, 30,  5,  TRUE),
  ('mix_100_15', 'Комбо M',      1690,  'RUB', 0, 100, 15, TRUE),
  ('mix_300_40', 'Комбо L',      4390,  'RUB', 0, 300, 40, TRUE)
ON CONFLICT (code) DO UPDATE
SET title = EXCLUDED.title,
    price_rub = EXCLUDED.price_rub,
    currency = EXCLUDED.currency,
    text_credits = EXCLUDED.text_credits,
    image_credits = EXCLUDED.image_credits,
    video_credits = EXCLUDED.video_credits,
    is_active = EXCLUDED.is_active,
    updated_at = now();

-- Deactivate legacy default packages from the first release.
UPDATE packages
SET is_active = FALSE,
    updated_at = now()
WHERE code IN ('starter', 'media', 'protxt');
