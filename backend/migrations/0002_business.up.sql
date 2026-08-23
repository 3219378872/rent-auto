-- business core schema

CREATE TABLE templates (
    hash_name      text PRIMARY KEY,
    display_name   text NOT NULL DEFAULT '',
    category       text NOT NULL DEFAULT '',
    uu_template_id bigint,
    uu_mark_price  numeric(12,2),
    eco_ref_price  numeric(12,2),
    value_anchor   numeric(12,2),
    anchor_updated_at timestamptz,
    blacklisted    boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE inventory_items (
    id             bigserial PRIMARY KEY,
    channel        text NOT NULL CHECK (channel IN ('uu','eco')),
    asset_id       text NOT NULL,
    hash_name      text NOT NULL REFERENCES templates(hash_name),
    market_hash_name text NOT NULL DEFAULT '',
    template_id    bigint,
    mark_price     numeric(12,2),
    tradable       boolean NOT NULL DEFAULT false,
    status         text NOT NULL DEFAULT 'in_stock',
    abrade         numeric(8,6),
    cost_basis     numeric(12,2),
    cost_source    text NOT NULL DEFAULT 'manual',
    raw            jsonb,
    last_synced_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (channel, asset_id)
);
CREATE INDEX idx_inventory_status ON inventory_items (status);
CREATE INDEX idx_inventory_hash ON inventory_items (hash_name);

CREATE TABLE listings (
    id             bigserial PRIMARY KEY,
    channel        text NOT NULL CHECK (channel IN ('uu','eco')),
    asset_id       text NOT NULL DEFAULT '',
    hash_name      text NOT NULL REFERENCES templates(hash_name),
    goods_ref      text NOT NULL,
    desired_state  text NOT NULL DEFAULT 'none' CHECK (desired_state IN ('none','active','delisted')),
    actual_state   text NOT NULL DEFAULT 'unknown' CHECK (actual_state IN ('unknown','none','active','leased','stale')),
    rent_price     numeric(12,2),
    long_rent_price numeric(12,2),
    max_days       integer,
    deposit        numeric(12,2),
    strategy_id    bigint,
    listed_at      timestamptz,
    last_reprice_at timestamptz,
    actual_synced_at timestamptz,
    UNIQUE (channel, goods_ref)
);
CREATE INDEX idx_listings_desired ON listings (desired_state);

CREATE TABLE lease_orders (
    id             bigserial PRIMARY KEY,
    channel        text NOT NULL CHECK (channel IN ('uu','eco')),
    order_ref      text NOT NULL,
    asset_id       text NOT NULL DEFAULT '',
    hash_name      text NOT NULL DEFAULT '',
    order_type     text NOT NULL DEFAULT 'short',
    status         text NOT NULL DEFAULT 'leasing',
    rent_days      integer NOT NULL DEFAULT 0,
    rent_price     numeric(12,2) NOT NULL DEFAULT 0,
    order_amount   numeric(12,2) NOT NULL DEFAULT 0,
    deposits       numeric(12,2) NOT NULL DEFAULT 0,
    started_at     timestamptz,
    due_at         timestamptz,
    finished_at    timestamptz,
    income_recorded boolean NOT NULL DEFAULT false,
    raw            jsonb,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (channel, order_ref)
);
CREATE INDEX idx_orders_status ON lease_orders (status);
CREATE INDEX idx_orders_finished ON lease_orders (finished_at DESC);

CREATE TABLE market_snapshots (
    id          bigserial PRIMARY KEY,
    hash_name   text NOT NULL REFERENCES templates(hash_name),
    source      text NOT NULL CHECK (source IN ('uu_market','eco_dump','own_order')),
    kind        text NOT NULL CHECK (kind IN ('lease_short','lease_long','deposit','sell')),
    rank        integer NOT NULL DEFAULT 0,
    price       numeric(12,2) NOT NULL,
    captured_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_snap_lookup ON market_snapshots (hash_name, kind, captured_at DESC);

CREATE TABLE strategies (
    id             bigserial PRIMARY KEY,
    name           text NOT NULL,
    enabled        boolean NOT NULL DEFAULT true,
    scope          text NOT NULL DEFAULT 'global' CHECK (scope IN ('global','template')),
    hash_name      text REFERENCES templates(hash_name),
    channel_route  text NOT NULL DEFAULT 'both' CHECK (channel_route IN ('uu_only','eco_only','both','uu_primary_eco_fallback')),
    params         jsonb NOT NULL DEFAULT '{}',
    priority       integer NOT NULL DEFAULT 0,
    real_execution_enabled boolean NOT NULL DEFAULT false,
    updated_by     text NOT NULL DEFAULT 'system',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uniq_template_strategy ON strategies (scope, hash_name) WHERE scope='template';

CREATE TABLE price_actions (
    id          bigserial PRIMARY KEY,
    ts          timestamptz NOT NULL DEFAULT now(),
    channel     text NOT NULL CHECK (channel IN ('uu','eco')),
    hash_name   text NOT NULL DEFAULT '',
    asset_id    text NOT NULL DEFAULT '',
    listing_id  bigint,
    action      text NOT NULL CHECK (action IN ('publish','reprice','delist','skip')),
    old_rent    numeric(12,2), new_rent numeric(12,2),
    old_long    numeric(12,2), new_long numeric(12,2),
    old_days    integer, new_days integer,
    old_deposit numeric(12,2), new_deposit numeric(12,2),
    decision    jsonb,
    dry_run     boolean NOT NULL DEFAULT true,
    success     boolean NOT NULL DEFAULT false,
    error       text
);
CREATE INDEX idx_actions_ts ON price_actions (ts DESC);

CREATE TABLE fund_flows (
    id          bigserial PRIMARY KEY,
    channel     text NOT NULL CHECK (channel IN ('uu','eco')),
    flow_ref    text NOT NULL,
    amount      numeric(12,2) NOT NULL,
    type        text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL,
    raw         jsonb,
    UNIQUE (channel, flow_ref)
);

CREATE TABLE daily_stats (
    stat_date   date NOT NULL,
    channel     text NOT NULL CHECK (channel IN ('uu','eco')),
    category    text NOT NULL DEFAULT '',
    income      numeric(14,2) NOT NULL DEFAULT 0,
    order_count integer NOT NULL DEFAULT 0,
    avg_rent_yield numeric(10,6),
    asset_snapshot jsonb,
    PRIMARY KEY (stat_date, channel, category)
);
