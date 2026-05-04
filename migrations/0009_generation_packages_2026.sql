-- Replace public generation packages with separate photo/video bundles
-- and refresh the subscription boost price.

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
  'combo_s', 'combo_m',
  'base_minimum', 'golden_middle', 'luxury_maximum',
  'photo_base_minimum', 'photo_golden_middle', 'photo_luxury_maximum',
  'video_base_minimum', 'video_golden_middle', 'video_luxury_maximum',
  'boost_10_2'
);

INSERT INTO packages(code, title, price_rub, currency, text_credits, image_credits, video_credits, is_active)
VALUES
  ('photo_base_minimum',    'Базовый минимум | Фото',       99, 'RUB', 0,  5,  0, TRUE),
  ('photo_golden_middle',   'Золотая середина | Фото',     199, 'RUB', 0, 15,  0, TRUE),
  ('photo_luxury_maximum',  'Роскошный максимум | Фото',   499, 'RUB', 0, 40,  0, TRUE),
  ('video_base_minimum',    'Базовый минимум | Видео',     249, 'RUB', 0,  0,  3, TRUE),
  ('video_golden_middle',   'Золотая середина | Видео',    599, 'RUB', 0,  0,  9, TRUE),
  ('video_luxury_maximum',  'Роскошный максимум | Видео', 1199, 'RUB', 0,  0, 15, TRUE),
  ('boost_10_2',            'Буст: +10 фото и 2 видео',    299, 'RUB', 0, 10,  2, TRUE)
ON CONFLICT (code) DO UPDATE
SET title = EXCLUDED.title,
    price_rub = EXCLUDED.price_rub,
    currency = EXCLUDED.currency,
    text_credits = EXCLUDED.text_credits,
    image_credits = EXCLUDED.image_credits,
    video_credits = EXCLUDED.video_credits,
    is_active = EXCLUDED.is_active,
    updated_at = now();
