FROM postgis/postgis:18-3.6

RUN apt-get update \
    && apt-get install -y --no-install-recommends "postgresql-${PG_MAJOR}-pgvector=0.8.6-*" \
    && rm -rf /var/lib/apt/lists/*
