"""Render croniter's previous and next slots over many expressions and instants, as golden data for the Go cron adapter."""

import json
import sys
from datetime import UTC
from datetime import datetime
from pathlib import Path
from typing import Any

from croniter import croniter

EXPRESSIONS = [
    "* * * * *",
    "*/5 * * * *",
    "0 * * * *",
    "30 2 * * *",
    "0 0 1 * *",
    "0 0 1 1 *",
    "0 0 * * 0",
    "0 0 * * 7",
    "15,45 * * * *",
    "0 9-17 * * 1-5",
    "*/15 9-17 * * *",
    "0 0 1,15 * *",
    "0 0 */2 * *",
    "0 */6 * * *",
    "5 4 * * sun",
    "0 0 1 jan *",
    "0 0 29 2 *",
    "0 0 31 * *",
    "0 0 30 2 *",
    "0 0 1 * 1",
    "0 0 15 * 5",
    "0 12 1-7 * 1",
    "0 0 13 * fri",
    "0 0 */10 * 1",
    "0 0 */2 * 1",
    "0 0 */1 * 1",
    "0 0 1 * */2",
    "0 0 1-31/10 * 1",
    "0 0 ? * 1",
    "0 0 1-5 * 0,6",
    "0 0 L * *",
    "0 0 * * 5#2",
    "10-50/10 * * * *",
    "0 0 1 */3 *",
    "59 23 31 12 *",
    "0 0 * 2 *",
    "@yearly",
    "@annually",
    "@monthly",
    "@weekly",
    "@daily",
    "@midnight",
    "@hourly",
    "  0   0 * *  *  ",
    "60 * * * *",
    "* * * *",
    "a b c d e",
    "0 24 * * *",
    "*/0 * * * *",
]

INSTANTS = [
    datetime(2024, 1, 1, 0, 0, 0, tzinfo=UTC),
    datetime(2023, 12, 31, 23, 59, 59, tzinfo=UTC),
    datetime(2024, 2, 29, 23, 59, 59, tzinfo=UTC),
    datetime(2024, 3, 15, 10, 30, 0, tzinfo=UTC),
    datetime(2024, 3, 15, 10, 30, 17, tzinfo=UTC),
    datetime(2024, 3, 15, 10, 30, 0, 500000, tzinfo=UTC),
    datetime(2024, 12, 31, 23, 59, 0, tzinfo=UTC),
    datetime(2025, 2, 28, 12, 0, 0, tzinfo=UTC),
    datetime(2024, 7, 4, 0, 0, 0, tzinfo=UTC),
    datetime(2026, 11, 27, 15, 4, 5, tzinfo=UTC),
    datetime(2028, 2, 29, 0, 0, 0, tzinfo=UTC),
    datetime(2024, 5, 31, 0, 0, 0, tzinfo=UTC),
]


def slot(expression: str, now: datetime, direction: str) -> dict[str, Any]:
    try:
        iterator = croniter(expression, now)
        found = iterator.get_prev(datetime) if direction == "prev" else iterator.get_next(datetime)
    except Exception as error:
        return {"error": type(error).__name__}
    return {"at": found.astimezone(UTC).isoformat()}


def main() -> None:
    out_path = Path(sys.argv[1])
    cases = [
        {
            "expression": expression,
            "now": now.isoformat(),
            "prev": slot(expression, now, "prev"),
            "next": slot(expression, now, "next"),
        }
        for expression in EXPRESSIONS
        for now in INSTANTS
    ]
    out_path.write_text(json.dumps(cases, indent=1, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
