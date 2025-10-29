-- seed.sql
-- Начальные данные (DML) для проекта ai-telebot
-- Запускать после применения schema.sql
-- Повторный запуск безопасен за счёт ON CONFLICT

BEGIN;

-- ---------- AI MODELS ----------
-- provider = ENUM(provider_kind), gen = ENUM(gen_type)
INSERT INTO ai_models(provider, model_code, gen, max_input, max_output, price_meta, enabled)
VALUES
  ('openai',     'gpt-4o-mini',        'text', 16000, 4000,  '{"cost_per_1k_tokens":0.03}'::jsonb, TRUE),
  ('openai',     'gpt-image-1',        'image', NULL,  NULL, '{}'::jsonb, TRUE),
  ('google',     'gemini-1.5-pro',     'text', 128000, 8192, '{}'::jsonb, TRUE),
  ('google',     'imagen-3',           'image', NULL,  NULL, '{}'::jsonb, TRUE),
  ('perplexity', 'perplexity-online',  'search', NULL, NULL, '{}'::jsonb, TRUE),
  ('alibaba',    'qwen-2.5',           'text',  32000, 4096, '{}'::jsonb, TRUE),
  ('bytedance',  'sora-like',          'video', NULL,  NULL, '{}'::jsonb, TRUE),
  ('anthropic',  'claude-3.5-sonnet',  'text', 200000, 8192, '{}'::jsonb, TRUE)
ON CONFLICT (provider, model_code) DO NOTHING;

-- ---------- PACKAGES ----------
-- 1) Базовый пакет: 5 видео, 5 фото, 10 текста
INSERT INTO packages(code, name, description, items_json, price_rub, currency, active)
VALUES (
  'pkg_basic',
  'Базовый пакет',
  '5 видео, 5 фото, 10 текста',
  '{"video":5,"image":5,"text":10,"search":0}',
  499,
  'RUB',
  TRUE
)
ON CONFLICT (code) DO NOTHING;

-- 2) Продвинутый пакет: 10 видео, 10 фото
INSERT INTO packages(code, name, description, items_json, price_rub, currency, active)
VALUES (
  'pkg_plus',
  'Продвинутый пакет',
  '10 видео, 10 фото',
  '{"video":10,"image":10,"text":0,"search":0}',
  899,
  'RUB',
  TRUE
)
ON CONFLICT (code) DO NOTHING;

-- 3) Текст+поиск 100: 100 генераций текста и 100 поиска
INSERT INTO packages(code, name, description, items_json, price_rub, currency, active)
VALUES (
  'pkg_text100',
  'Текст + Поиск 100',
  '100 генераций текста и 100 поиска',
  '{"text":100,"search":100,"image":0,"video":0}',
  999,
  'RUB',
  TRUE
)
ON CONFLICT (code) DO NOTHING;

-- ---------- CONFIG ----------
-- Дефолтные бесплатные лимиты на месяц
INSERT INTO config_kv(key, value, updated_at)
VALUES (
  'free_quota_defaults',
  '{"text":10,"search":10,"image":3,"video":1}',
  now()
)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, updated_at = now();

-- Политика расчёта «месяца» для бесплатных лимитов (TZ можно сменить)
INSERT INTO config_kv(key, value, updated_at)
VALUES (
  'free_quota_policy',
  '{"timezone":"Europe/Moscow","reset_period":"monthly"}',
  now()
)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, updated_at = now();

-- Параметры YooKassa по умолчанию (для удобства в коде)
INSERT INTO config_kv(key, value, updated_at)
VALUES (
  'payment_provider_yookassa',
  '{
     "provider":"yookassa",
     "currency":"RUB",
     "capture_mode":"manual",
     "description":"AI Telebot пакет",
     "confirmation_type":"redirect"
   }',
  now()
)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, updated_at = now();

COMMIT;