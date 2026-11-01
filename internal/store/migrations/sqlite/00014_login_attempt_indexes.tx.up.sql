-- The credential statistics group every login attempt by password, by
-- pair (with whether it was accepted), and the accepted pairs: read in
-- the order of these indexes, which hold all they read, they
-- need no sort of millions of rows.

CREATE INDEX ix_login_attempts_password ON login_attempts (password);

--bun:split

CREATE INDEX ix_login_attempts_username_password_success ON login_attempts (username, password, success);

--bun:split

CREATE INDEX ix_login_attempts_success_username_password ON login_attempts (success, username, password);
