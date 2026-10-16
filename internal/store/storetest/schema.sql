-- Schema of the Django-owned tables, from pg_dump --schema-only of production.
-- Used by integration tests to recreate the tables the Go binary shares with Django.

CREATE TABLE public.main_about (
    id integer NOT NULL,
    text text NOT NULL,
    html text NOT NULL,
    published boolean NOT NULL,
    title character varying(30) NOT NULL
);

CREATE SEQUENCE public.main_about_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.main_about_id_seq OWNED BY public.main_about.id;

CREATE TABLE public.main_generatedq (
    id integer NOT NULL,
    text character varying(280) NOT NULL,
    slug character varying(280) NOT NULL,
    displayed date NOT NULL,
    tweeted boolean NOT NULL,
    votes integer NOT NULL,
    tweet_time timestamp with time zone NOT NULL,
    randomized integer NOT NULL,
    reddit_id character varying(30) NOT NULL,
    submit_time timestamp with time zone NOT NULL,
    submitted boolean NOT NULL,
    CONSTRAINT main_generatedq_randomized_check CHECK ((randomized >= 0))
);

CREATE SEQUENCE public.main_generatedq_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.main_generatedq_id_seq OWNED BY public.main_generatedq.id;

ALTER TABLE ONLY public.main_about ALTER COLUMN id SET DEFAULT nextval('public.main_about_id_seq'::regclass);

ALTER TABLE ONLY public.main_generatedq ALTER COLUMN id SET DEFAULT nextval('public.main_generatedq_id_seq'::regclass);

ALTER TABLE ONLY public.main_about
    ADD CONSTRAINT main_about_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.main_about
    ADD CONSTRAINT main_about_title_c132dcdb_uniq UNIQUE (title);

ALTER TABLE ONLY public.main_generatedq
    ADD CONSTRAINT main_generatedq_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.main_generatedq
    ADD CONSTRAINT main_generatedq_slug_key UNIQUE (slug);

CREATE INDEX main_about_title_c132dcdb_like ON public.main_about USING btree (title varchar_pattern_ops);

CREATE INDEX main_generatedq_slug_6bb04186_like ON public.main_generatedq USING btree (slug varchar_pattern_ops);

