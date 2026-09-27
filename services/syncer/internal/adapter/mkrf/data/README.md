# Cultural events dataset (prepared)

- Source: Ministry of Culture of the Russian Federation, open dataset
  "Мероприятия в сфере культуры", https://opendata.mkrf.ru/opendata/7705851331-events
- Version: 12, published 2026-05-07 (`Last-Modified: Thu, 07 May 2026 22:57:09 GMT`).
- Terms: standard terms of use of open data of the Russian Federation
  (http://data.gov.ru/information-usage); the source must be credited.
- Extracted on 2026-09-27 by streaming all 643 parts of `data-12-structure-2.json.zip`.
- Filter: at least one place in Moscow or Perm (`fullAddress` contains "г Москва"/"г Пермь"
  or `locale.name` is "Москва"/"Пермь") and at least one seance whose end is after
  2026-09-26T21:43Z. Because the filter used the seance end, a few events have only
  past seances with abnormally long windows.
- Redaction: e-mail addresses and phone numbers inside string values are replaced with
  `[email]` and `[phone]`, because free-text descriptions contain staff contacts.
- Format: one event record per line, gzip. Records were re-serialized when extracted, so
  the bytes are those of this file, not of the original archive.
- Contents: 163 events — 156 in Moscow, 7 in Perm.
