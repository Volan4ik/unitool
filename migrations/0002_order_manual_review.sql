DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'order_status') THEN
    ALTER TYPE order_status ADD VALUE IF NOT EXISTS 'manual_review';
  END IF;
END$$;
