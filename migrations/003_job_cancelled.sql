-- issue #22: отмена заданий сбора из UI. Новое состояние jobs.state
-- 'cancelled' (аддитивно: расширен CHECK, колонки не меняются).
-- Отменяются pending/retry (ещё не стартовали) и running (воркер и
-- collect-конвейер опрашивают JobCancelled и останавливаются).
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_state_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_state_check
  CHECK (state IN ('pending','running','retry','done','dead','cancelled'));
