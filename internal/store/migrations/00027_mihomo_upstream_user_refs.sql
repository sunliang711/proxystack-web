-- +goose Up
ALTER TABLE mihomo_upstreams ADD COLUMN ref_user_id INTEGER REFERENCES users(id);
ALTER TABLE mihomo_upstreams ADD COLUMN ref_profile_id INTEGER REFERENCES user_profiles(id);

-- +goose Down
ALTER TABLE mihomo_upstreams DROP COLUMN ref_profile_id;
ALTER TABLE mihomo_upstreams DROP COLUMN ref_user_id;
