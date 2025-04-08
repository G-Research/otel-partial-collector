SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', 'public', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;
SET default_tablespace = '';
SET default_table_access_method = heap;
SET TIME ZONE 'UTC';


CREATE TABLE partial_traces (
    trace_id text NOT NULL,
    span_id text NOT NULL,
    trace bytea NOT NULL,
    "timestamp" timestamp with time zone DEFAULT now() NOT NULL,
    "expires_at" timestamp with time zone NOT NULL
);

ALTER TABLE ONLY partial_traces
    ADD CONSTRAINT partial_traces_pkey PRIMARY KEY (span_id, trace_id);

CREATE INDEX idx_partial_traces_expires_at ON partial_traces USING btree (expires_at);
