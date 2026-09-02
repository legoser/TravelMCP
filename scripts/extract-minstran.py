#!/usr/bin/env python3
"""Извлекает из XLSX-реестра Минтранса маршруты заданных регионов.

Вход: путь к XLSX (реестр действующих межрегиональных маршрутов регулярных
перевозок), коды регионов, дата выгрузки. Выход: JSON с маршрутами, остановками
и расписанием (зимний/летний блоки, списки рейсов через ';').

Роли: разведка данных (Этап 0) и seed для Go-парсера (Этап 1). Использование:
  python3 scripts/extract-minstran.py reestr.xlsx 22,42,54,70 --snapshot 2026-06-16 -o data/reestr/regions.json
"""

import argparse
import json
import os
import re
import sys
import urllib.parse
import urllib.request
import zipfile
from collections import OrderedDict

from lxml import etree

NS = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
COL = re.compile(r"([A-Z]+)(\d+)")

ROUTE = 0
ORDER = 1
RNAME = 2
CARRIER = 3
STOP = 3
STOP_REGION = 4
OP_REG = 5
W_DAYS = 6
W_DEP = 7
W_DWELL = 8
W_ARR_DAYS = 9
W_ARR = 10
W_PERIOD = 11
S_DAYS = 12
S_DEP = 13
S_DWELL = 14
S_ARR_DAYS = 15
S_ARR = 16
S_PERIOD = 17


def colidx(ref):
    letters = COL.match(ref).group(1)
    n = 0
    for ch in letters:
        n = n * 26 + (ord(ch) - 64)
    return n - 1


def sheet_rows(path, name):
    z = zipfile.ZipFile(path)
    shared = []
    if "xl/sharedStrings.xml" in z.namelist():
        ss = etree.fromstring(z.read("xl/sharedStrings.xml"))
        shared = ["".join(t for t in si.xpath(".//m:t/text()", namespaces=NS)) for si in ss]
    root = etree.fromstring(z.read("xl/worksheets/{0}.xml".format(name)))
    data = root.find(".//m:sheetData", namespaces=NS)
    for r in data.findall("m:row", namespaces=NS):
        d = {}
        for c in r.findall("m:c", namespaces=NS):
            v = c.find("m:v", namespaces=NS)
            if v is None or v.text is None:
                continue
            d[colidx(c.get("r"))] = shared[int(v.text)] if c.get("t") == "s" else v.text
        yield d


def split_times(field):
    if not field or "нет" in field:
        return None
    out = []
    for tok in field.split(";"):
        tok = tok.strip()
        out.append(None if not tok or tok == "нет" else tok)
    return out


def period_block(d, base):
    dep = split_times(d.get(base + 1))
    arr = split_times(d.get(base + 4))
    return {
        "days": d.get(base, "").strip() or None,
        "dep": dep or [],
        "dwell": split_times(d.get(base + 2)) or [],
        "arr": arr or [],
        "period": d.get(base + 5, "").strip() or None,
    }


WEEKDAY_MAP = {
    "пн": 1, "пон": 1, "понедельник": 1,
    "вт": 2, "вторник": 2,
    "ср": 3, "среда": 3,
    "чт": 4, "четверг": 4,
    "пт": 5, "пятница": 5,
    "сб": 6, "суббота": 6,
    "вс": 0, "воскресенье": 0, "вск": 0,
}


def parse_days(days_str):
    if not days_str:
        return list(range(7))
    s = days_str.strip().lower().replace("\u0451", "\u0435")
    if s in ("ежедневно", "ежедневн", "ежедневный"):
        return list(range(7))
    if s in ("через день", "1 через 1", "1через1"):
        return list(range(7))
    if s in ("нет", "нет отправлений", "нет отправлении"):
        return []
    s = s.replace(";", ",").replace(" ", ",")
    weekdays = set()
    for tok in s.split(","):
        tok = tok.strip().strip(".")
        if not tok:
            continue
        if "-" in tok and not tok[0].isdigit():
            parts = tok.split("-")
            if len(parts) == 2:
                a = WEEKDAY_MAP.get(parts[0].strip())
                b = WEEKDAY_MAP.get(parts[1].strip())
                if a is not None and b is not None:
                    cur = a
                    while True:
                        weekdays.add(cur % 7)
                        if cur % 7 == b % 7:
                            break
                        cur = (cur + 1) % 7
                        if len(weekdays) > 7:
                            break
                    continue
        if tok.isdigit():
            try:
                n = int(tok)
                if 1 <= n <= 31:
                    continue
            except ValueError:
                pass
        wd = WEEKDAY_MAP.get(tok)
        if wd is not None:
            weekdays.add(wd)
            continue
        m = re.match(r"^(\d+)\s*через\s*(\d+)$", tok)
        if m:
            weekdays.update(range(7))
            continue
    if not weekdays:
        return list(range(7))
    return sorted(weekdays)


def parse_period(period_str, snapshot=""):
    if not period_str or period_str.strip().lower() == "круглогодично":
        year = snapshot[:4] if snapshot and len(snapshot) >= 4 and snapshot[:4].isdigit() else "2026"
        return "{0}-01-01".format(year), "{0}-12-31".format(year)
    m = re.search(r"(\d{2})\.(\d{2}).*?(\d{2})\.(\d{2})", period_str)
    if m:
        d1, m1, d2, m2 = m.groups()
        year = snapshot[:4] if snapshot and len(snapshot) >= 4 and snapshot[:4].isdigit() else "2026"
        start = "{0}-{1}-{2}".format(year, m1, d1)
        end = "{0}-{1}-{2}".format(year, m2, d2)
        return start, end
    year = snapshot[:4] if snapshot and len(snapshot) >= 4 and snapshot[:4].isdigit() else "2026"
    return "{0}-01-01".format(year), "{0}-12-31".format(year)


STOPWORDS = set("""оп остановочный пункт автовокзал автостанция автобусная станция
ас ав дкп г с п р.п рп пгт пов кассовый аэропорт межд города вокзал название
транспортный остановка""".split())


# Проверенные координаты для городов/сёл, где автоматический OSM/газетерный
# метчинг двусмыслен (одноимённые населённые пункты в разных регионах) либо
# данных нет вовсе. Значения для "yandex:" получены через Яндекс-Геокодер и
# сохраняются в кэш; "osm:" — сверены с локальным OSM датасетом.
# Расширено 2026-09-02: все столицы регионов (85) + крупные нестоличные города
# (>300k или райцентры) — отправная точка покрытия всей страны без API-затрат.
CITY_OVERRIDE = {
    "болотное": ("osm", 55.6468, 84.3594),
    "прокопьевск": ("osm", 53.9061, 86.7450),
    "рубцовск": ("osm", 51.5155, 81.2030),
    "панкрушиха": ("osm", 53.8356, 80.3481),
    "хабары": ("osm", 53.6203, 79.5354),
    "березовка": ("osm", 53.40, 83.99),
    "топучая": ("osm", 51.13, 85.59),
    "хабаровка": ("osm", 50.68, 86.29),
    "камень-на-оби": ("yandex", 53.7915, 81.3546),
    "майма": ("yandex", 52.0048, 85.9021),
    "киселевск": ("yandex", 54.0061, 86.6367),
    "стрежевой": ("yandex", 60.7329, 77.6040),
    "павловск": ("yandex", 53.3135, 82.9895),
    "топки": ("osm", 55.3416, 86.0610),
    "турочак": ("osm", 52.2556, 87.1153),
    "туран": ("yandex", 51.64, 93.90),
    "майкоп": ("osm", 44.60667, 40.10778),
    "уфа": ("osm", 54.7352, 55.9587),
    "улан-удэ": ("osm", 51.8345, 107.5846),
    "горно-алтайск": ("osm", 51.9578, 85.9617),
    "махачкала": ("osm", 42.9849, 47.5047),
    "магас": ("osm", 43.1667, 44.8167),
    "нальчик": ("osm", 43.4853, 43.6071),
    "элиста": ("osm", 46.3080, 44.2704),
    "черкесск": ("osm", 44.2233, 42.0574),
    "петрозаводск": ("osm", 61.7850, 34.3469),
    "сыктывкар": ("osm", 61.6687, 50.8353),
    "йошкар-ола": ("osm", 56.6388, 47.8908),
    "саранск": ("osm", 54.1842, 45.1817),
    "якутск": ("osm", 62.0355, 129.6755),
    "владикавказ": ("osm", 43.0205, 44.6819),
    "казань": ("osm", 55.7887, 49.1221),
    "кызыл": ("osm", 51.7189, 94.4378),
    "ижевск": ("osm", 56.8526, 53.2116),
    "абакан": ("osm", 53.7220, 91.4432),
    "грозный": ("osm", 43.3178, 45.6982),
    "чебоксары": ("osm", 56.1439, 47.2489),
    "барнаул": ("osm", 53.3481, 83.7754),
    "краснодар": ("osm", 45.0355, 38.9753),
    "красноярск": ("osm", 56.0158, 92.8932),
    "владивосток": ("osm", 43.1332, 131.9113),
    "ставрополь": ("osm", 45.0428, 41.9734),
    "хабаровск": ("osm", 48.4808, 135.0777),
    "благовещенск": ("osm", 50.2907, 127.5272),
    "архангельск": ("osm", 64.5401, 40.5433),
    "астрахань": ("osm", 46.3497, 48.0408),
    "белгород": ("osm", 50.5956, 36.5873),
    "брянск": ("osm", 53.2521, 34.3717),
    "владимир": ("osm", 56.1290, 40.4075),
    "волгоград": ("osm", 48.7080, 44.5133),
    "вологда": ("osm", 58.1939, 39.6401),
    "воронеж": ("osm", 51.6673, 39.2003),
    "иваново": ("osm", 57.0003, 40.9737),
    "иркутск": ("osm", 52.2869, 104.2806),
    "калининград": ("osm", 54.7104, 20.4521),
    "калуга": ("osm", 54.5072, 36.2527),
    "петропавловск-камчатский": ("osm", 53.0438, 158.6453),
    "кемерово": ("osm", 55.3552, 86.0861),
    "киров": ("osm", 58.6036, 49.6680),
    "кострома": ("osm", 57.7666, 40.9266),
    "курган": ("osm", 55.4404, 65.3411),
    "курск": ("osm", 51.7303, 36.1930),
    "гатчина": ("osm", 59.5653, 30.1228),
    "липецк": ("osm", 52.6088, 39.5990),
    "магадан": ("osm", 59.5608, 150.8006),
    "красногорск": ("osm", 55.8207, 37.3297),
    "мурманск": ("osm", 68.9585, 33.0827),
    "нижний новгород": ("osm", 56.2965, 43.9361),
    "великий новгород": ("osm", 58.5215, 31.2755),
    "новосибирск": ("osm", 55.0084, 82.9357),
    "омск": ("osm", 54.9885, 73.3242),
    "оренбург": ("osm", 51.7682, 55.0970),
    "орел": ("osm", 52.9672, 36.0696),
    "пенза": ("osm", 53.2006, 45.0046),
    "пермь": ("osm", 58.0104, 56.2502),
    "псков": ("osm", 57.8136, 28.3496),
    "ростов-на-дону": ("osm", 47.2225, 39.7187),
    "рязань": ("osm", 54.6269, 39.6915),
    "самара": ("osm", 53.2415, 50.2212),
    "саратов": ("osm", 51.5406, 46.0087),
    "южно-сахалинск": ("osm", 46.9581, 142.7430),
    "екатеринбург": ("osm", 56.8389, 60.6057),
    "смоленск": ("osm", 54.7826, 32.0453),
    "тамбов": ("osm", 52.7212, 41.4523),
    "тверь": ("osm", 56.8587, 35.9006),
    "томск": ("osm", 56.4977, 84.9744),
    "тула": ("osm", 54.1931, 37.6173),
    "тюмень": ("osm", 57.1522, 65.5272),
    "ульяновск": ("osm", 54.3142, 48.3630),
    "челябинск": ("osm", 55.1600, 61.4026),
    "чита": ("osm", 52.0336, 113.4990),
    "ярославль": ("osm", 57.6261, 39.8845),
    "москва": ("osm", 55.7558, 37.6176),
    "санкт-петербург": ("osm", 59.9343, 30.3351),
    "биробиджан": ("osm", 48.7930, 132.9291),
    "симферополь": ("osm", 44.9572, 34.1108),
    "ханты-мансийск": ("osm", 61.0042, 69.0357),
    "анадырь": ("osm", 64.7342, 177.5101),
    "салехард": ("osm", 66.5299, 66.6145),
    "севастополь": ("osm", 44.6167, 33.5254),
    "нарьян-мар": ("osm", 67.6380, 53.0069),
    "набережные челны": ("osm", 55.7027, 52.3141),
    "сочи": ("osm", 43.5855, 39.7203),
    "новороссийск": ("osm", 44.7230, 37.7687),
    "пятигорск": ("osm", 44.0486, 43.0594),
    "новокузнецк": ("osm", 53.7557, 87.1099),
    "таганрог": ("osm", 47.2123, 38.9353),
    "тольятти": ("osm", 53.5303, 49.3461),
    "нижний тагил": ("osm", 57.9190, 60.0069),
    "магнитогорск": ("osm", 53.4129, 58.9791),
    "сургут": ("osm", 61.2540, 73.3964),
    "нижневартовск": ("osm", 60.9385, 76.5585),
    "волжский": ("osm", 48.7875, 44.7695),
    "балашиха": ("osm", 55.8094, 37.9581),
    "подольск": ("osm", 55.4231, 37.5447),
    "химки": ("osm", 55.8899, 37.4453),
    "сызрань": ("osm", 53.1605, 48.4742),
    "каменск-уральский": ("osm", 56.4145, 61.9170),
    "златоуст": ("osm", 55.1713, 59.6508),
    "нефтеюганск": ("osm", 61.0880, 72.6037),
    "березники": ("osm", 59.4101, 56.8214),
    "альметьевск": ("osm", 54.9010, 52.3045),
    "волгодонск": ("osm", 47.5167, 42.1570),
    "череповец": ("osm", 59.1343, 37.9014),
}

YANDEX_CACHE = "data/reestr/yandex_geo.json"
YANDEX_GEO = "https://geocode-maps.yandex.ru/1.x/"
YANDEX_DAILY_LIMIT = 500

NOMINATIM_CACHE = "data/reestr/nominatim_geo.json"
NOMINATIM_GEO = "https://nominatim.openstreetmap.org/search"
NOMINATIM_DAILY_LIMIT = 10000  # фактически безлимит, но throttle 1 req/s по политике OSM

# согласованный регион -> примерный центр (для геокодера и REGION_KW запросов)
REGION_KW = {
    "01": "Республика Адыгея", "02": "Республика Башкортостан", "03": "Республика Бурятия",
    "04": "Республика Алтай", "05": "Республика Дагестан", "06": "Республика Ингушетия",
    "07": "Кабардино-Балкарская Республика", "08": "Республика Калмыкия", "09": "Карачаево-Черкесская Республика",
    "10": "Республика Карелия", "11": "Республика Коми", "12": "Республика Марий Эл",
    "13": "Республика Мордовия", "14": "Республика Саха (Якутия)", "15": "Республика Северная Осетия-Алания",
    "16": "Республика Татарстан", "17": "Республика Тыва", "18": "Удмуртская Республика",
    "19": "Республика Хакасия", "20": "Чеченская Республика", "21": "Чувашская Республика",
    "22": "Алтайский край", "23": "Краснодарский край", "24": "Красноярский край",
    "25": "Приморский край", "26": "Ставропольский край", "27": "Хабаровский край",
    "28": "Амурская область", "29": "Архангельская область", "30": "Астраханская область",
    "31": "Белгородская область", "32": "Брянская область", "33": "Владимирская область",
    "34": "Волгоградская область", "35": "Вологодская область", "36": "Воронежская область",
    "37": "Ивановская область", "38": "Иркутская область", "39": "Калининградская область",
    "40": "Калужская область", "41": "Камчатский край", "42": "Кемеровская область",
    "43": "Кировская область", "44": "Костромская область", "45": "Курганская область",
    "46": "Курская область", "47": "Ленинградская область", "48": "Липецкая область",
    "49": "Магаданская область", "50": "Московская область", "51": "Мурманская область",
    "52": "Нижегородская область", "53": "Новгородская область", "54": "Новосибирская область",
    "55": "Омская область", "56": "Оренбургская область", "57": "Орловская область",
    "58": "Пензенская область", "59": "Пермский край", "60": "Псковская область",
    "61": "Ростовская область", "62": "Рязанская область", "63": "Самарская область",
    "64": "Саратовская область", "65": "Сахалинская область", "66": "Свердловская область",
    "67": "Смоленская область", "68": "Тамбовская область", "69": "Тверская область",
    "70": "Томская область", "71": "Тульская область", "72": "Тюменская область",
    "73": "Ульяновская область", "74": "Челябинская область", "75": "Забайкальский край",
    "76": "Ярославская область", "77": "город Москва", "78": "город Санкт-Петербург",
    "79": "Еврейская автономная область", "82": "Республика Крым", "83": "Ненецкий АО",
    "86": "Ханты-Мансийский АО", "87": "Чукотский АО", "89": "Ямало-Ненецкий АО",
    "92": "город Севастополь",
}


class YandexGeoCoder:
    """Яндекс-Геокодер: лимит 500/сутки, кэш yandex_geo.json, ротация с Nominatim."""

    def __init__(self, cache_path=YANDEX_CACHE, limit=YANDEX_DAILY_LIMIT):
        self.cache_path = cache_path
        self.limit = limit
        self.data = {}
        self.used = 0
        try:
            with open(cache_path, encoding="utf-8") as f:
                self.data = json.load(f)
        except OSError:
            self.data = {}
        self.key = ""
        envf = ".env"
        try:
            if os.path.exists(envf):
                for line in open(envf, encoding="utf-8"):
                    if line.strip().startswith("YANDEX_GEOCODE_KEY="):
                        self.key = line.split("=", 1)[1].strip().strip("\"'")
        except OSError:
            pass
        if not self.key:
            self.key = os.environ.get("YANDEX_GEOCODE_KEY", "")

    def lookup(self, query):
        """Возвращает (lat, lon) для query или None. Кэширует по query."""
        if query in self.data:
            return tuple(self.data[query])
        if not self.key or self.used >= self.limit:
            return None
        try:
            url = YANDEX_GEO + "?format=json&results=1&apikey=" + urllib.parse.quote(self.key) \
                + "&geocode=" + urllib.parse.quote(query)
            with urllib.request.urlopen(url, timeout=15) as r:
                body = json.load(r)
            fm = body.get("response", {}).get("GeoObjectCollection", {}).get("featureMember", [])
            if not fm:
                return None
            pos = fm[0]["GeoObject"]["Point"]["pos"].split()
            lon, lat = float(pos[0]), float(pos[1])
            self.used += 1
            self.data[query] = [lat, lon]
            return (lat, lon)
        except Exception:
            return None

    def save(self):
        if not self.data:
            return
        try:
            os.makedirs(os.path.dirname(self.cache_path), exist_ok=True)
            with open(self.cache_path, "w", encoding="utf-8") as f:
                json.dump(self.data, f, ensure_ascii=False, indent=1)
        except OSError:
            pass


class NominatimGeoCoder:
    """Nominatim (OSM) — дешевле и отказоустойчивее, лимит 1 req/s, кэш nominatim_geo.json.
    Основной провайдер в ротации: сначала Nominatim, затем Yandex как fallback.
    """

    def __init__(self, cache_path=NOMINATIM_CACHE, limit=NOMINATIM_DAILY_LIMIT):
        self.cache_path = cache_path
        self.limit = limit
        self.data = {}
        self.used = 0
        self.last_ts = 0
        try:
            with open(cache_path, encoding="utf-8") as f:
                self.data = json.load(f)
        except OSError:
            self.data = {}

    def lookup(self, query):
        """Возвращает (lat, lon) для query или None. Кэширует, throttles 1 req/s."""
        if query in self.data:
            return tuple(self.data[query])
        if self.used >= self.limit:
            return None
        # throttle 1 req/s по политике Nominatim
        import time as _time
        now = _time.time()
        if self.last_ts and now - self.last_ts < 1.1:
            _time.sleep(1.1 - (now - self.last_ts))
        try:
            params = urllib.parse.urlencode({
                "q": query, "format": "json", "limit": 1, "accept-language": "ru"
            })
            url = NOMINATIM_GEO + "?" + params
            req = urllib.request.Request(url, headers={
                "User-Agent": "travelmcp/1.0 (travelmcp@example.com)"
            })
            with urllib.request.urlopen(req, timeout=15) as r:
                body = json.load(r)
            if not body or not isinstance(body, list) or len(body) == 0:
                return None
            lat = float(body[0].get("lat", 0))
            lon = float(body[0].get("lon", 0))
            if not lat or not lon:
                return None
            self.used += 1
            self.last_ts = _time.time()
            self.data[query] = [lat, lon]
            return (lat, lon)
        except Exception:
            return None

    def save(self):
        if not self.data:
            return
        try:
            os.makedirs(os.path.dirname(self.cache_path), exist_ok=True)
            with open(self.cache_path, "w", encoding="utf-8") as f:
                json.dump(self.data, f, ensure_ascii=False, indent=1)
        except OSError:
            pass


def geocode_stops(stops, osm_path, gazetteer_path):
    """Обогащает остановки координатами (OSM-метчинг + газетир + Nominatim→Yandex ротация).

    Порядок: CITY_OVERRIDE якоря (все столицы) → точное OSM → газетир →
    Nominatim (дешево, 1 req/s, кэш) → Yandex (500/сутки, кэш) ротация.
    Остановки без уверенного совпадения оставляем без координат (lat/lon = 0).
    """
    stopwords = STOPWORDS

    def norm(s):
        s = s.lower().replace("\u0451", "\u0435")
        for ch in "\u00ab\u00bb\"()[],.:\u2014\u2013/+":
            s = s.replace(ch, " ")
        return " ".join(s.split())

    def tokens(s):
        return [t for t in norm(s).split() if t and t not in stopwords]

    exact = {}
    osm_bus = []
    try:
        with open(osm_path, encoding="utf-8") as f:
            osm = json.load(f)
        for o in osm:
            n = o.get("name")
            if not n:
                continue
            nn = norm(n)
            exact.setdefault(nn, (o.get("lat", 0), o.get("lon", 0)))
            if "автовокзал" in nn or "автостанция" in nn:
                osm_bus.append((nn, o.get("lat", 0), o.get("lon", 0)))
    except OSError as e:
        print("geocode: не удалось прочитать OSM {0}: {1}".format(osm_path, e),
              file=sys.stderr)
        return stops

    g = []
    try:
        with open(gazetteer_path, encoding="utf-8") as f:
            places = json.load(f)
        for p in places:
            g.append((norm(p["name"]), p.get("lat", 0), p.get("lon", 0)))
            for a in p.get("aliases", []):
                g.append((norm(a), p.get("lat", 0), p.get("lon", 0)))
    except OSError as e:
        print("geocode: не удалось прочитать газетир {0}: {1}".format(
            gazetteer_path, e), file=sys.stderr)
        return stops

    def related(tok, gn):
        if tok == gn:
            return True
        if tok.startswith(gn) or gn.startswith(tok):
            return True
        c = common_prefix(tok, gn)
        return c >= 3 and abs(len(tok) - len(gn)) <= 4 and c * 2 >= min(len(tok), len(gn))

    def common_prefix(a, b):
        n = 0
        for x, y in zip(a, b):
            if x != y:
                break
            n += 1
        return n

    ADJ_SUFFIXES = ("ичевский", "иевский", "ковский", "евский", "овский",
                    "инский", "енский", "ской", "ский", "ская", "ское",
                    "ой", "ый", "ий", "ая", "ое")

    def city_stem(tok):
        for suf in ADJ_SUFFIXES:
            if tok.endswith(suf) and len(tok) - len(suf) >= 3:
                return tok[:-len(suf)]
        return tok

    def stem_related(tok, gn):
        return related(city_stem(tok), gn)

    def is_generic(tok):
        return tok in stopwords or tok in (
            "пов", "дкп", "ост", "остоп", "село", "деревня", "поселок", "города")

    def city_hint(name):
        """Возвращает город-основу из имени остановки или None."""
        toks = [t for t in tokens(name) if not is_generic(t) and len(t) >= 3]
        if not toks:
            return None
        best = max(toks, key=len)
        stem = city_stem(best)
        if stem in CITY_OVERRIDE:
            return stem
        return best

    def override_coord(stem):
        rec = CITY_OVERRIDE.get(stem)
        return (rec[1], rec[2]) if rec else None

    yc = YandexGeoCoder()

    def match(name, region_code):
        nn = norm(name)
        if nn in exact:
            return exact[nn]
        low = norm(name)
        toks = tokens(name) or [nn]
        hint = city_hint(name)
        # 0. Проверенный якорь города (CITY_OVERRIDE) — детерминированно, раньше
        #    всех скаттеров. Для двусмысленных/отсутствующих городов (Березовка →
        #    Берёзовка/Берёзовский, Камень-на-Оби и т.п.) автоподбор по имени может
        #    утянуть в другой регион, поэтому такие города якорями фиксируются явно.
        override = override_coord(city_stem(hint)) if hint else None
        if override:
            return override

        def is_terminal_name(low):
            toks_ = low.split()
            if any("автовокзал" in t or "автостанция" in t for t in toks_):
                return True
            return any(t == "ав" or t == "авт" or t == "а/в" for t in toks_)

        best = None
        bl = 0
        for t in toks:
            if len(t) < 3:
                continue
            for gn, lat, lon in g:
                if related(t, gn) and len(gn) > bl:
                    bl = len(gn)
                    best = (lat, lon)
        if best:
            return best
        for onn, lat, lon in osm_bus:
            hit = [t for t in toks if len(t) >= 3 and (t in onn or related(t, onn))]
            score = sum(len(t) for t in hit)
            if hit and score > bl:
                bl = score
                best = (lat, lon)
        if best:
            return best
        for gn, lat, lon in g:
            # gn должен быть отдельным словом в nn, а не подстрокой внутри него:
            # иначе газетир "Станция" цепляется к любому "*станция*" (автостАнция)
            # и тянет остановку на координаты своей записи.
            if gn and len(gn) > bl and gn in toks:
                bl = len(gn)
                best = (lat, lon)
        if best:
            return best
        for t in toks:
            if len(t) >= 3 and t in exact:
                # скаттер по точному узлу OSM — хорош для однозначных сёл
                # (Сростки, Манжерок), но для одноимённых в разных регионах
                # (Березовка→Берёзовский) координаты перекрыты шагом 0 выше.
                return exact[t]
        if is_terminal_name(low):
            tks = [t for t in toks if len(t) >= 3]
            best = None
            bl = 0
            for onn, lat, lon in osm_bus:
                hit = [t for t in tks if stem_related(t, onn)]
                score = sum(len(t) for t in hit)
                if hit and score > bl:
                    bl = score
                    best = (lat, lon)
            if best:
                return best
            for gn, lat, lon in g:
                if gn and len(gn) > bl and any(stem_related(t, gn) for t in tks):
                    bl = len(gn)
                    best = (lat, lon)
            return best
        return None

    def plausible(lat, lon):
        if not (lat and lon):
            return False
        return abs(lon) > 20 and abs(lat) < 70

    yc = YandexGeoCoder()
    nc = NominatimGeoCoder()
    enable_external = os.environ.get("ENABLE_GEOCODE") == "1" or os.environ.get("ENABLE_EXTERNAL_GEOCODE") == "1"
    found = 0
    y_used = 0
    n_used = 0
    for s in stops:
        region_code = s.get("region", "")
        hint = city_hint(s["name"])
        hstem = city_stem(hint) if hint else None
        c = match(s["name"], region_code)
        if c and plausible(*c):
            s["lat"] = c[0]
            s["lon"] = c[1]
            found += 1
            continue
        if not enable_external:
            # без ENABLE_GEOCODE — используем только OSM/газетир/CITY_OVERRIDE якоря
            # для якорей yandex без внешнего геокодера оставляем без координат
            continue
        # Ротация при ENABLE_GEOCODE=1: сначала Nominatim (дешево), затем Yandex 500/сутки
        if hint and hint.strip():
            q = "Россия, " + REGION_KW.get(region_code, "") + ", " + hint
            if hstem and hstem in CITY_OVERRIDE and CITY_OVERRIDE[hstem][0] == "yandex":
                got = yc.lookup(q)
                if got:
                    s["lat"] = got[0]; s["lon"] = got[1]; found += 1; y_used += 1
                    continue
                got = nc.lookup(q)
                if got:
                    s["lat"] = got[0]; s["lon"] = got[1]; found += 1; n_used += 1
                    continue
            else:
                got = nc.lookup(q)
                if got and plausible(*got):
                    s["lat"] = got[0]; s["lon"] = got[1]; found += 1; n_used += 1
                    continue
                got = yc.lookup(q)
                if got and plausible(*got):
                    s["lat"] = got[0]; s["lon"] = got[1]; found += 1; y_used += 1
                    continue
    yc.save()
    nc.save()
    print("geocode: координат получено {0} из {1} (nominatim={2}, yandex={3}, used y/n {4}/{5})".format(found, len(stops), n_used, y_used, yc.used, nc.used),
          file=sys.stderr)
    return stops


def main():
    ap = argparse.ArgumentParser(description="Реестр Минтранса (XLSX) → JSON датасет")
    ap.add_argument("--in", dest="xlsx", required=True, help="путь к XLSX-реестру")
    ap.add_argument("--regions", default="22,42,54,70",
                    help="коды регионов через запятую, напр. 22,42,54,70 или 'all' для всех регионов")
    ap.add_argument("--snapshot", default="", help="дата выгрузки, напр. 2026-06-16")
    ap.add_argument("--osm", default="", help="OSM-датасет stops.json для геокодинга")
    ap.add_argument("--gazetteer", default="internal/geo/places.json",
                    help="газетир городов для геокодинга")
    ap.add_argument("--out", dest="output", required=True, help="путь к JSON")
    args = ap.parse_args()

    raw_regions = args.regions.strip().lower()
    if raw_regions in ("all", "*", ""):
        regions = None  # все регионы
    else:
        regions = set(x.strip() for x in args.regions.split(",") if x.strip())
        if "all" in regions:
            regions = None

    routes = {}
    for d in sheet_rows(args.xlsx, "sheet1"):
        reg = d.get(ROUTE, "")
        if not reg or reg.startswith("Регистрационный номер"):
            continue
        order = d.get(ORDER, 0)
        try:
            order = int(order or 0) or None
        except ValueError:
            order = None
        routes[reg] = OrderedDict([("order", order), ("name", d.get(RNAME, "")),
                                   ("carrier", d.get(CARRIER, ""))])
    perevoz = {}
    for d in sheet_rows(args.xlsx, "sheet3"):
        reg = d.get(ROUTE, "")
        if not reg or reg.startswith("Регистрационный номер"):
            continue
        perevoz[reg] = d

    # проход 1: какие маршруты затронуты хотя бы одной остановкой в регионах
    touched = set()
    for sheet in ("sheet4", "sheet5"):
        for d in sheet_rows(args.xlsx, sheet):
            rreg = d.get(ROUTE, "")
            if not rreg or rreg.startswith("Регистрационный"):
                continue
            stop_reg = d.get(STOP_REGION, "")
            if regions is None or stop_reg in regions:
                touched.add(rreg)
    # фильтр пустых рег
    touched.discard("")
    touched.discard(None)

    # проход 2: по затронутым маршрутам собираем ВСЕ их остановки
    stops = OrderedDict()

    def stop_ref(name, region, opreg):
        key = (name, region)
        if key not in stops:
            sid = "op:{0}:{1}".format(region, opreg) if opreg else "nr:" + name
            stops[key] = {"id": sid, "name": name, "region": region, "op_reg": opreg}
        return stops[key]["id"]

    by_route = OrderedDict()
    for sheet, direction in (("sheet4", "forward"), ("sheet5", "backward")):
        for d in sheet_rows(args.xlsx, sheet):
            rreg = d.get(ROUTE, "")
            if rreg not in touched:
                continue
            stop_id = stop_ref(d.get(STOP, ""), d.get(STOP_REGION, ""), d.get(OP_REG, ""))
            by_route.setdefault((rreg, direction), []).append((stop_id, d))

    service_map = OrderedDict()
    next_sid = 1

    def get_service_id(days_str, period_str):
        nonlocal next_sid
        key = ((days_str or "").strip(), (period_str or "").strip())
        if key not in service_map:
            start, end = parse_period(period_str, args.snapshot)
            weekdays = parse_days(days_str)
            service_map[key] = {
                "id": next_sid,
                "name": days_str or "ежедневно",
                "start_date": start,
                "end_date": end,
                "weekdays": weekdays,
            }
            next_sid += 1
        return service_map[key]["id"]

    sched_out = []
    for (rreg, direction), entries in by_route.items():
        stops_list = []
        svc_days = None
        svc_period = None
        for stop_id, d in entries:
            wb = period_block(d, W_DAYS)
            sb = period_block(d, S_DAYS)
            if svc_days is None:
                if wb.get("days"):
                    svc_days = wb["days"]
                    svc_period = wb.get("period")
                elif sb.get("days"):
                    svc_days = sb["days"]
                    svc_period = sb.get("period")
            stops_list.append({
                "stop": stop_id,
                "region": d.get(STOP_REGION, ""),
                "winter": wb,
                "summer": sb,
            })
        sid = get_service_id(svc_days, svc_period)
        sched_out.append({"route": rreg, "direction": direction, "service_id": sid, "stops": stops_list})

    routes_out = []
    for reg in sorted(touched):
        if not reg:
            continue
        info = routes.get(reg, {})
        row = {"reg": reg, "name": info.get("name", ""), "order": info.get("order"),
               "carrier": info.get("carrier", "")}
        pv = perevoz.get(reg)
        if pv:
            row["carrier_inn"] = pv.get(3, "")
        routes_out.append(row)

    stops_out = []
    for (name, region), s in stops.items():
        if name:
            stops_out.append(s)

    if args.osm:
        stops_out = geocode_stops(stops_out, args.osm, args.gazetteer)

    services_out = []
    service_days_out = []
    for key, svc in service_map.items():
        services_out.append({
            "id": svc["id"],
            "name": svc["name"],
            "start_date": svc["start_date"],
            "end_date": svc["end_date"],
        })
        for wd in svc["weekdays"]:
            service_days_out.append({"service_id": svc["id"], "weekday": wd})

    result = {
        "source": "minstran_reestr",
        "snapshot": args.snapshot,
        "regions": sorted(regions) if regions is not None else ["all"],
        "routes": routes_out,
        "stops": stops_out,
        "services": services_out,
        "service_days": service_days_out,
        "service_exceptions": [],
        "schedules": sched_out,
    }

    blob = json.dumps(result, ensure_ascii=False, indent=1)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as f:
            f.write(blob)
    else:
        print(blob)

    print("маршрутов touched: {0}, остановок: {1}, блоков расписания: {2}".format(
        len(touched), len(stops_out), len(sched_out)), file=sys.stderr)


if __name__ == "__main__":
    main()