-- 0004_seed.sql
-- Идемпотентные начальные данные под текущую схему.

BEGIN;

-- Базовые пакеты (дублируют schema, но позволяют обновить значения при повторном прогоне)
INSERT INTO packages(code, title, price_rub, text_credits, image_credits, video_credits, is_active)
VALUES
  ('starter', 'Стартовый пакет', 199, 50, 0, 0, TRUE),
  ('media',   'Фото+Видео',       499, 0, 10, 5, TRUE),
  ('protxt',  'Текст PRO',       1490, 300, 0, 0, TRUE)
ON CONFLICT (code) DO UPDATE
SET title = EXCLUDED.title,
    price_rub = EXCLUDED.price_rub,
    text_credits = EXCLUDED.text_credits,
    image_credits = EXCLUDED.image_credits,
    video_credits = EXCLUDED.video_credits,
    is_active = TRUE,
    updated_at = now();

COMMIT;
