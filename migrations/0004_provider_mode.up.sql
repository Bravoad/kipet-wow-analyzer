-- A database belongs to one provider mode. Bootstrap binds an empty database;
-- populated unmarked databases require an explicit provenance decision.
CREATE TABLE app_settings (
    key text PRIMARY KEY,
    value text NOT NULL
);
