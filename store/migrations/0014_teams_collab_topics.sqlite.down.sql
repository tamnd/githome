-- 0014_teams_collab_topics (sqlite, down)
DROP TABLE IF EXISTS collaborators;
DROP TABLE IF EXISTS team_repos;
DROP TABLE IF EXISTS team_members;
DROP TABLE IF EXISTS teams;
ALTER TABLE repositories DROP COLUMN topics;
