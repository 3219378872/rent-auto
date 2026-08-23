-- +migrate Up (marker comment informational; runner uses filename convention)
CREATE TABLE app_settings (
    key         text PRIMARY KEY,
    value_enc   bytea,
    value_plain jsonb,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id      bigserial PRIMARY KEY,
    ts      timestamptz NOT NULL DEFAULT now(),
    actor   text NOT NULL,
    channel text,
    action  text NOT NULL,
    target  text,
    detail  jsonb
);

CREATE INDEX idx_audit_log_ts ON audit_log (ts DESC);
CREATE INDEX idx_audit_log_action ON audit_log (action);
