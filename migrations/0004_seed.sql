-- 0004_seed.sql
-- Идемпотентные начальные данные под текущую схему.

BEGIN;

-- Базовые пакеты (дублируют schema, но позволяют обновить значения при повторном прогоне)
INSERT INTO packages(code, title, price_rub, text_credits, image_credits, video_credits, search_credits, is_active)
VALUES
  ('starter', 'Стартовый пакет', 199, 50, 0, 0, 0, TRUE),
  ('media',   'Фото+Видео',       499, 0, 10, 5, 0, TRUE),
  ('protxt',  'Текст PRO',       1490, 300, 0, 0, 0, TRUE)
ON CONFLICT (code) DO UPDATE
SET title = EXCLUDED.title,
    price_rub = EXCLUDED.price_rub,
    text_credits = EXCLUDED.text_credits,
    image_credits = EXCLUDED.image_credits,
    video_credits = EXCLUDED.video_credits,
    search_credits = EXCLUDED.search_credits,
    is_active = TRUE,
    updated_at = now();

-- Тестовый админский пользователь с начальными кредитами
WITH admin_user AS (
    INSERT INTO users (tg_id, username, first_name, last_name, is_admin)
    VALUES (999000111, 'unitool_admin', 'Unitool', 'Admin', TRUE)
    ON CONFLICT (tg_id) DO UPDATE
        SET username = EXCLUDED.username,
            first_name = EXCLUDED.first_name,
            last_name = EXCLUDED.last_name,
            is_admin = TRUE,
            updated_at = now()
    RETURNING id
)
INSERT INTO credit_ledger (user_id, delta_text, delta_image, delta_video, delta_search, reason, meta)
SELECT
    admin_user.id,
    200, 20, 5, 80,
    'admin_grant',
    jsonb_build_object('seed_tag', 'dev_admin_grant')
FROM admin_user
WHERE NOT EXISTS (
    SELECT 1 FROM credit_ledger
    WHERE user_id = admin_user.id
      AND meta ->> 'seed_tag' = 'dev_admin_grant'
);

COMMIT;
