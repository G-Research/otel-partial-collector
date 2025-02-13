--
-- PostgreSQL database dump
--

-- Dumped from database version 17.2 (Debian 17.2-1.pgdg120+1)
-- Dumped by pg_dump version 17.2 (Ubuntu 17.2-1.pgdg24.04+1)

-- Started on 2025-02-05 16:04:59 CET

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

--
-- TOC entry 217 (class 1259 OID 16389)
-- Name: partial_traces; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.partial_traces (
    key text NOT NULL,
    value bytea NOT NULL,
    "timestamp" timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE public.partial_traces OWNER TO postgres;

--
-- TOC entry 3211 (class 2606 OID 16396)
-- Name: partial_traces partial_traces_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.partial_traces
    ADD CONSTRAINT partial_traces_pkey PRIMARY KEY (key);


-- Completed on 2025-02-05 16:04:59 CET

--
-- PostgreSQL database dump complete
--

