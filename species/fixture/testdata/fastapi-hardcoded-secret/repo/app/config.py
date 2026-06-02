"""A FastAPI settings module with one hardcoded secret and two safe assignments.

`SECRET_KEY` is assigned a string LITERAL (an obvious fake placeholder, never a
real credential), so the app's signing key is committed to repo history — the
leak fastapi-hardcoded-secret flags (propose-only). The llm fix removes the
literal, reads the value from the environment (`os.environ["SECRET_KEY"]`), and
records the variable in `.env.example`.

`DATABASE_URL` is a non-secret-named target, and `API_KEY` is already read from
the environment (`os.getenv(...)`), so both are correctly NOT flagged — proving
the rule targets only a secret-named target assigned a string literal and leaves
env-backed values and non-secret config alone.
"""

import os

SECRET_KEY = "changeme-not-a-real-secret"

DATABASE_URL = "sqlite:///./app.db"

API_KEY = os.getenv("API_KEY")
