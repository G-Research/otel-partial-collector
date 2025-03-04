SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;


SET default_tablespace = '';

SET default_table_access_method = heap;

CREATE TABLE public.partial_traces (
    trace_id text NOT NULL,
    span_id text NOT NULL,
    value bytea NOT NULL,
    "timestamp" timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE public.partial_traces OWNER TO postgres;

ALTER TABLE ONLY public.partial_traces
    ADD CONSTRAINT partial_traces_pkey PRIMARY KEY (span_id, trace_id);
