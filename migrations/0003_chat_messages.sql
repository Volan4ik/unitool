CREATE TABLE IF NOT EXISTS chat_messages (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id),
  conversation_id UUID NOT NULL,
  kind TEXT NOT NULL,          -- 'text' | 'search' | 'image' | 'video'
  role TEXT NOT NULL,          -- 'user' | 'assistant' | 'system'
  content_text TEXT,
  attachment_url TEXT,
  provider TEXT,
  model TEXT,
  input_tokens INT,
  output_tokens INT,
  generation_request_id BIGINT REFERENCES generation_requests(id),
  created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_u_c_k
  ON chat_messages(user_id, conversation_id, kind, id DESC);

