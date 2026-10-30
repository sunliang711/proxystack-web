-- +goose Up
CREATE TABLE users (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    position         INTEGER NOT NULL,
    name             TEXT    NOT NULL UNIQUE,
    enabled          INTEGER NOT NULL,
    uuid             TEXT    NOT NULL,
    ss_password      TEXT    NOT NULL,
    remark           TEXT    NOT NULL,
    display_template TEXT    NOT NULL,
    tag              TEXT    NOT NULL,
    sub_token        TEXT    NOT NULL UNIQUE,
    quota_bytes      INTEGER NOT NULL,
    reset_mode       TEXT    NOT NULL,
    reset_day        INTEGER NOT NULL,
    expire_at        TEXT    NOT NULL,
    note             TEXT    NOT NULL
);

CREATE TABLE user_profiles (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    position         INTEGER NOT NULL,
    name             TEXT    NOT NULL,
    uuid             TEXT    NOT NULL,
    ss_password      TEXT    NOT NULL,
    remark           TEXT    NOT NULL,
    display_template TEXT    NOT NULL,
    tag              TEXT    NOT NULL,
    UNIQUE (user_id, name)
);

CREATE TABLE stacks (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    position                INTEGER NOT NULL,
    name                    TEXT    NOT NULL UNIQUE,
    enabled                 INTEGER NOT NULL,
    desired_running         INTEGER NOT NULL,
    role                    TEXT    NOT NULL,
    labels                  TEXT    NOT NULL,
    xray_enabled            INTEGER NOT NULL,
    xray_loglevel           TEXT    NOT NULL,
    xray_api                TEXT    NOT NULL,
    xray_stats              TEXT    NOT NULL,
    xray_policy             TEXT    NOT NULL,
    xray_api_port           INTEGER NOT NULL,
    outbound_type           TEXT    NOT NULL,
    outbound_server         TEXT    NOT NULL,
    outbound_port           INTEGER NOT NULL,
    outbound_username       TEXT    NOT NULL,
    outbound_password       TEXT    NOT NULL,
    outbound_private_direct INTEGER NOT NULL,
    mihomo_enabled          INTEGER NOT NULL,
    mihomo_mode             TEXT    NOT NULL,
    mihomo_loglevel         TEXT    NOT NULL,
    controller_port         INTEGER NOT NULL,
    controller_secret       TEXT    NOT NULL,
    rule_profile            TEXT    NOT NULL,
    rule_final              TEXT    NOT NULL,
    rule_extra              TEXT    NOT NULL
);

CREATE TABLE inbounds (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    stack_id          INTEGER NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    position          INTEGER NOT NULL,
    name              TEXT    NOT NULL,
    protocol          TEXT    NOT NULL,
    listen            TEXT    NOT NULL,
    port              INTEGER NOT NULL,
    sub               INTEGER NOT NULL,
    udp               INTEGER NOT NULL,
    network           TEXT    NOT NULL,
    ws_path           TEXT    NOT NULL,
    ws_headers        TEXT    NOT NULL,
    grpc_service_name TEXT    NOT NULL,
    method            TEXT    NOT NULL,
    password          TEXT    NOT NULL,
    auth_type         TEXT    NOT NULL,
    auth_username     TEXT    NOT NULL,
    auth_password     TEXT    NOT NULL,
    server            TEXT    NOT NULL,
    remark            TEXT    NOT NULL,
    display_template  TEXT    NOT NULL,
    region            TEXT    NOT NULL,
    tag               TEXT    NOT NULL,
    UNIQUE (stack_id, name)
);

CREATE TABLE inbound_user_refs (
    inbound_id       INTEGER NOT NULL REFERENCES inbounds (id) ON DELETE CASCADE,
    position         INTEGER NOT NULL,
    user_id          INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    profile_id       INTEGER REFERENCES user_profiles (id) ON DELETE CASCADE,
    remark           TEXT    NOT NULL,
    display_template TEXT    NOT NULL,
    tag              TEXT    NOT NULL,
    PRIMARY KEY (inbound_id, position)
);

CREATE TABLE mihomo_listeners (
    stack_id INTEGER NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    kind     TEXT    NOT NULL,
    enabled  INTEGER NOT NULL,
    name     TEXT    NOT NULL,
    listen   TEXT    NOT NULL,
    port     INTEGER NOT NULL,
    auth     TEXT    NOT NULL,
    accounts TEXT    NOT NULL,
    PRIMARY KEY (stack_id, kind)
);

CREATE TABLE mihomo_upstreams (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    stack_id       INTEGER NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    position       INTEGER NOT NULL,
    name           TEXT    NOT NULL,
    type           TEXT    NOT NULL,
    ref_inbound_id INTEGER REFERENCES inbounds (id),
    raw_config     TEXT    NOT NULL
);

CREATE TABLE mihomo_groups (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    stack_id INTEGER NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    name     TEXT    NOT NULL,
    type     TEXT    NOT NULL,
    proxies  TEXT    NOT NULL,
    url      TEXT    NOT NULL,
    interval INTEGER NOT NULL,
    strategy TEXT    NOT NULL
);

CREATE TABLE config_revisions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at INTEGER NOT NULL,
    actor      TEXT    NOT NULL,
    summary    TEXT    NOT NULL,
    snapshot   TEXT    NOT NULL
);

-- +goose Down
DROP TABLE config_revisions;
DROP TABLE mihomo_groups;
DROP TABLE mihomo_upstreams;
DROP TABLE mihomo_listeners;
DROP TABLE inbound_user_refs;
DROP TABLE inbounds;
DROP TABLE stacks;
DROP TABLE user_profiles;
DROP TABLE users;
