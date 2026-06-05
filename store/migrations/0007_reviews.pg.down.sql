-- 0007_reviews (postgres, down): drop the code review surface, children first.
DROP TABLE pull_request_check_state;
DROP TABLE check_runs;
DROP TABLE check_suites;
DROP TABLE commit_statuses;
DROP TABLE pull_request_review_comments;
DROP TABLE pull_request_reviews;
