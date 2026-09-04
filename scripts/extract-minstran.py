#!/usr/bin/env python3
"""Извлекает из XLSX-реестра Минтранса маршруты заданных регионов.

Вход: путь к XLSX (реестр действующих межрегиональных маршрутов регулярных
перевозок), коды регионов, дата выгрузки. Выход: JSON с маршрутами, остановками
и расписанием (зимний/летний блоки, списки рейсов через ';').

Роли: разведка данных (Этап 0) и seed для Go-парсера (Этап 1). Использование:
  python3 scripts/extract-minstran.py reestr.xlsx 22,42,54,70 --snapshot 2026-06-16 -o data/reestr/regions.json
"""

import argparse
import hashlib
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


TRANSLIT_MAP = {
    'а': 'a', 'б': 'b', 'в': 'v', 'г': 'g', 'д': 'd', 'е': 'e', 'ё': 'yo', 'ж': 'zh',
    'з': 'z', 'и': 'i', 'й': 'y', 'к': 'k', 'л': 'l', 'м': 'm', 'н': 'n', 'о': 'o',
    'п': 'p', 'р': 'r', 'с': 's', 'т': 't', 'у': 'u', 'ф': 'f', 'х': 'kh', 'ц': 'ts',
    'ч': 'ch', 'ш': 'sh', 'щ': 'shch', 'ъ': '', 'ы': 'y', 'ь': '', 'э': 'e', 'ю': 'yu', 'я': 'ya',
    'А': 'A', 'Б': 'B', 'В': 'V', 'Г': 'G', 'Д': 'D', 'Е': 'E', 'Ё': 'Yo', 'Ж': 'Zh',
    'З': 'Z', 'И': 'I', 'Й': 'Y', 'К': 'K', 'Л': 'L', 'М': 'M', 'Н': 'N', 'О': 'O',
    'П': 'P', 'Р': 'R', 'С': 'S', 'Т': 'T', 'У': 'U', 'Ф': 'F', 'Х': 'Kh', 'Ц': 'Ts',
    'Ч': 'Ch', 'Ш': 'Sh', 'Щ': 'Shch', 'Ъ': '', 'Ы': 'Y', 'Ь': '', 'Э': 'E', 'Ю': 'Yu', 'Я': 'Ya',
}


def translit(s):
    out = []
    for ch in s:
        if ch in TRANSLIT_MAP:
            out.append(TRANSLIT_MAP[ch])
        elif 'А' <= ch <= 'я' or ch in 'ёЁ':
            out.append('')
        else:
            out.append(ch)
    t = ''.join(out)
    t = re.sub(r'[^A-Za-z0-9]+', '-', t)
    t = re.sub(r'-+', '-', t).strip('-').lower()
    return t[:40] or 'stop'


def op_hash(name, region):
    h = hashlib.sha256((name + '|' + region).encode('utf-8')).hexdigest()[:8]
    return h


STOPWORDS = set("""оп остановочный пункт автовокзал автостанция автобусная станция
ас ав дкп г с п р.п рп пгт пов кассовый аэропорт межд города вокзал название
транспортный остановка""".split())


def _load_city_override():
    raw = {
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
    for _p in [os.environ.get("CITIES_PATH"), os.environ.get("CITIES_DATA_PATH"), os.environ.get("TRAVELMCP__CITIES__PATH"), "configs/cities.yaml", "internal/store/cities.yaml"]:
        if _p and os.path.exists(_p):
            try:
                import yaml as _y
                with open(_p, encoding="utf-8") as _f:
                    _d = _y.safe_load(_f) or {}
                _cities = _d.get("cities") if isinstance(_d, dict) else None
                if _cities:
                    _out = {}
                    for _c in _cities:
                        _name = str(_c.get("name", "")).strip().lower()
                        if not _name:
                            continue
                        try:
                            _lat = float(_c.get("lat", 0))
                            _lon = float(_c.get("lon", 0))
                        except Exception:
                            continue
                        _src = str(_c.get("source", "osm") or "osm")
                        _out[_name] = (_src, _lat, _lon)
                    if _out:
                        print(f"city_override: loaded {len(_out)} from {_p}", file=sys.stderr, flush=True)
                        return _out
            except Exception as _e:
                print(f"city_override: yaml load failed {_p}: {_e}", file=sys.stderr, flush=True)
                pass
    return raw


CITY_OVERRIDE = _load_city_override()

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

    def lookup(self, query, region_code=None):
        """Возвращает (lat, lon) для query или None. Кэширует только успешные ответы. Пробует results=5 и фильтрует по региону."""
        if query in self.data:
            v = self.data[query]
            if v is None:
                return None
            try:
                return tuple(v)
            except Exception:
                return None
        if not self.key or self.used >= self.limit:
            return None
        try:
            url = YANDEX_GEO + "?format=json&results=5&apikey=" + urllib.parse.quote(self.key) \
                + "&geocode=" + urllib.parse.quote(query)
            with urllib.request.urlopen(url, timeout=15) as r:
                body = json.load(r)
            fms = body.get("response", {}).get("GeoObjectCollection", {}).get("featureMember", [])
            if not fms:
                return None
            region_name = REGION_KW.get(region_code, "").lower() if region_code else ""
            best = None
            for fm in fms:
                try:
                    pos = fm["GeoObject"]["Point"]["pos"].split()
                    lon, lat = float(pos[0]), float(pos[1])
                    descr = fm["GeoObject"].get("description", "").lower()
                    name = fm["GeoObject"].get("name", "").lower()
                    meta = fm["GeoObject"].get("metaDataProperty", {}).get("GeocoderMetaData", {})
                    kind = meta.get("kind", "")
                    text = (descr + " " + name).lower()
                    if region_name and region_name.lower() not in text and kind not in ("locality", "province"):
                        if fm != fms[0]:
                            continue
                    best = (lat, lon)
                    if kind in ("locality", "district", "province"):
                        break
                    if best and fm == fms[0]:
                        pass
                except Exception:
                    continue
            if best is None:
                return None
            self.used += 1
            self.data[query] = [best[0], best[1]]
            return best
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

    def lookup(self, query, viewbox=None, region_code=None):
        """Возвращает (lat, lon) для query или None. Кэширует только успешные ответы, throttles 1 req/s. Поддерживает viewbox и фильтрацию по региону."""
        cache_key = query if not viewbox else query + "|vb:" + viewbox
        if cache_key in self.data:
            v = self.data[cache_key]
            if v is None:
                return None
            print("nominatim: cache hit {0!r} -> {1}".format(query, v), file=sys.stderr, flush=True)
            try:
                return tuple(v)
            except Exception:
                return None
        if query in self.data and not viewbox:
            v = self.data[query]
            if v is not None:
                print("nominatim: cache hit {0!r} -> {1}".format(query, v), file=sys.stderr, flush=True)
                try:
                    return tuple(v)
                except Exception:
                    return None
        if self.used >= self.limit:
            print("nominatim: limit hit {0}/{1} for {2!r}".format(self.used, self.limit, query), file=sys.stderr, flush=True)
            return None
        import time as _time
        now = _time.time()
        if self.last_ts and now - self.last_ts < 1.1:
            wait = 1.1 - (now - self.last_ts)
            print("nominatim: throttle sleep {0:.2f}s for {1!r}".format(wait, query), file=sys.stderr, flush=True)
            _time.sleep(wait)
        try:
            params = {
                "q": query, "format": "json", "limit": 5, "accept-language": "ru",
                "countrycodes": "ru", "addressdetails": 1, "extratags": 1
            }
            if viewbox:
                params["viewbox"] = viewbox
                params["bounded"] = 1
            url = NOMINATIM_GEO + "?" + urllib.parse.urlencode(params)
            print("nominatim: request {0!r} -> {1}".format(query, url[:160]), file=sys.stderr, flush=True)
            req = urllib.request.Request(url, headers={
                "User-Agent": "travelmcp/1.0 (https://github.com/anomalyco/travelmcp)"
            })
            t0 = _time.time()
            with urllib.request.urlopen(req, timeout=15) as r:
                body = json.load(r)
            print("nominatim: response {0!r} {1} items in {2:.2f}s".format(query, len(body) if isinstance(body, list) else type(body), _time.time()-t0), file=sys.stderr, flush=True)
            if not body or not isinstance(body, list) or len(body) == 0:
                return None
            best = None
            region_name = REGION_KW.get(region_code, "").lower() if region_code else ""
            for item in body:
                try:
                    lat = float(item.get("lat", 0)); lon = float(item.get("lon", 0))
                except Exception:
                    continue
                if not lat or not lon:
                    continue
                disp = (item.get("display_name") or "").lower()
                addr = item.get("address") or {}
                state = (addr.get("state") or addr.get("region") or "").lower()
                if region_name and region_name.lower() not in disp and region_name.lower() not in state:
                    if item != body[0]:
                        continue
                if item.get("class") == "amenity" and item.get("type") in ("bus_station", "bus_stop"):
                    best = (lat, lon)
                    break
                if best is None:
                    best = (lat, lon)
            if best is None:
                return None
            self.used += 1
            self.last_ts = _time.time()
            self.data[cache_key] = [best[0], best[1]]
            if cache_key != query:
                self.data[query] = [best[0], best[1]]
            return best
        except Exception as e:
            print("nominatim: error {0!r} -> {1}".format(query, e), file=sys.stderr, flush=True)
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
    import time as _gt
    _geocode_t0 = _gt.time()
    print("geocode: старт stops={0} osm={1} gazetteer={2}".format(len(stops), osm_path, gazetteer_path), file=sys.stderr, flush=True)
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
              file=sys.stderr, flush=True)
        return stops
    print("geocode: OSM loaded {0} exact={1} bus={2} elapsed={3:.1f}s".format(osm_path, len(exact), len(osm_bus), _gt.time()-_geocode_t0), file=sys.stderr, flush=True)

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
            gazetteer_path, e), file=sys.stderr, flush=True)
        return stops
    print("geocode: gazetteer loaded {0} entries={1} elapsed={2:.1f}s".format(gazetteer_path, len(g), _gt.time()-_geocode_t0), file=sys.stderr, flush=True)

    GENERIC_HINTS = set([
        "южный", "северный", "западный", "восточный", "центральный", "главный",
        "пригородный", "новый", "старый", "верхний", "нижний", "большой", "малый",
        "автопавильон", "автостанция", "автовокзал", "станция", "остановка",
        "диспетчерско", "кассовый", "поворот", "аэропорт", "вокзал",
    ])

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
            "пов", "дкп", "ост", "остоп", "село", "деревня", "поселок", "города") or tok in GENERIC_HINTS

    CITY_MARKERS = re.compile(r'\b(?:г\.?|с\.?|п\.?|д\.?|р\.п\.?|пос\.?|посёлок|село|деревня|поселок)\s+([А-ЯЁа-яё][А-ЯЁа-яё\- ]{2,})', re.I)

    def extract_city_explicit(name):
        m = CITY_MARKERS.search(name)
        if m:
            raw = m.group(1).strip()
            raw = re.sub(r'\s+', ' ', raw)
            raw = raw.split('/')[0].split(',')[0].strip()
            raw = raw.strip(' "«»()')
            parts = raw.split()
            if parts:
                cand = parts[0]
                if len(parts) > 1 and parts[1][0].isupper() and len(parts[1]) >= 3:
                    cand = parts[0] + " " + parts[1]
                return cand
        return None

    def city_hint(name):
        """Возвращает город-основу из имени остановки или None."""
        explicit = extract_city_explicit(name)
        if explicit:
            ex_norm = norm(explicit).split()
            if ex_norm:
                cand = ex_norm[0]
                if cand not in stopwords and cand not in GENERIC_HINTS and len(cand) >= 3:
                    return cand
                if len(ex_norm) > 1:
                    cand2 = ex_norm[1]
                    if cand2 not in stopwords and cand2 not in GENERIC_HINTS and len(cand2) >= 3:
                        return cand2
        toks = [t for t in tokens(name) if not is_generic(t) and len(t) >= 3]
        if not toks:
            return None
        for t in toks:
            if city_stem(t) in CITY_OVERRIDE:
                return t
        filtered = [t for t in toks if t not in GENERIC_HINTS and len(t) >= 4]
        if filtered:
            toks = filtered
        best = max(toks, key=len) if toks else None
        if best and best in GENERIC_HINTS:
            return None
        if best and len(best) < 4 and best not in CITY_OVERRIDE:
            return None
        return best

    def override_coord(stem):
        rec = CITY_OVERRIDE.get(stem)
        return (rec[1], rec[2]) if rec else None

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
                    # регион-фильтр для газетира: без него "Березовка" тянет в другой край
                    if not region_plausible(lat, lon, region_code):
                        continue
                    bl = len(gn)
                    best = (lat, lon)
        if best and region_plausible(best[0], best[1], region_code):
            return best
        for onn, lat, lon in osm_bus:
            hit = [t for t in toks if len(t) >= 3 and (t in onn or related(t, onn))]
            score = sum(len(t) for t in hit)
            if hit and score > bl:
                if not region_plausible(lat, lon, region_code):
                    continue
                bl = score
                best = (lat, lon)
        if best and region_plausible(best[0], best[1], region_code):
            return best
        for gn, lat, lon in g:
            # gn должен быть отдельным словом в nn, а не подстрокой внутри него:
            # иначе газетир "Станция" цепляется к любому "*станция*" (автостАнция)
            # и тянет остановку на координаты своей записи.
            if gn and len(gn) > bl and gn in toks:
                if not region_plausible(lat, lon, region_code):
                    continue
                bl = len(gn)
                best = (lat, lon)
        if best and region_plausible(best[0], best[1], region_code):
            return best
        for t in toks:
            if len(t) >= 3 and t in exact:
                # скаттер по точному узлу OSM — хорош для однозначных сёл
                # (Сростки, Манжерок), но для одноимённых в разных регионах
                # (Березовка→Берёзовский) координаты перекрыты шагом 0 выше.
                lat, lon = exact[t]
                if not region_plausible(lat, lon, region_code):
                    continue
                return (lat, lon)
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

    REGION_CENTER = {
        "22": (53.35, 83.78), "04": (51.96, 85.96), "42": (55.36, 86.08), "54": (55.01, 82.93), "70": (56.50, 84.97),
        "24": (56.01, 92.89), "26": (45.04, 41.97), "07": (43.49, 43.61), "02": (54.73, 55.95), "12": (56.64, 47.89),
        "16": (55.79, 49.12), "19": (53.72, 91.44), "66": (56.84, 60.60), "74": (55.16, 61.40), "50": (55.75, 37.61),
        "77": (55.75, 37.61), "78": (59.93, 30.33), "61": (47.22, 39.71), "23": (45.03, 38.97), "34": (48.70, 44.51),
        "36": (51.66, 39.20), "01": (44.60, 40.10), "03": (51.83, 107.58), "05": (42.98, 47.50), "06": (43.16, 44.81),
    }

    def region_viewbox(region_code):
        if region_code not in REGION_CENTER:
            return None
        clat, clon = REGION_CENTER[region_code]
        delta = 4.5 if region_code in ("24", "14", "28", "27", "86", "89") else 2.8
        return "{:.3f},{:.3f},{:.3f},{:.3f}".format(clon - delta, clat + delta, clon + delta, clat - delta)

    def region_plausible(lat, lon, region_code):
        if region_code in REGION_CENTER:
            clat, clon = REGION_CENTER[region_code]
            import math
            R = 6371
            dlat = (lat - clat) * math.pi / 180
            dlon = (lon - clon) * math.pi / 180
            a = math.sin(dlat/2)**2 + math.cos(clat*math.pi/180)*math.cos(lat*math.pi/180)*math.sin(dlon/2)**2
            d = 2 * R * math.asin(math.sqrt(a))
            limit = 800 if region_code in ("24", "14", "28", "27", "86", "89") else 600
            return d < limit
        return plausible(lat, lon)

    def plausible(lat, lon):
        if not (lat and lon):
            return False
        return abs(lon) > 20 and abs(lat) < 70

    yc = YandexGeoCoder()
    nc = NominatimGeoCoder()
    enable_external = os.environ.get("ENABLE_GEOCODE") == "1" or os.environ.get("ENABLE_EXTERNAL_GEOCODE") == "1"
    print("geocode: external={0} yandex_key={1} y_limit={2} n_cache={3} y_cache={4}".format(enable_external, bool(yc.key), yc.limit, len(nc.data), len(yc.data)), file=sys.stderr, flush=True)
    found = 0
    y_used = 0
    n_used = 0
    total = len(stops)
    for idx, s in enumerate(stops, 1):
        region_code = s.get("region", "")
        hint = city_hint(s["name"])
        if hint and (hint.lower() in GENERIC_HINTS or len(hint) < 4):
            hint = None
        hstem = city_stem(hint) if hint else None
        c = match(s["name"], region_code)
        if c and plausible(*c) and region_plausible(c[0], c[1], region_code):
            s["lat"] = c[0]
            s["lon"] = c[1]
            found += 1
            continue
        elif c and plausible(*c):
            print("geocode: [{0}/{1}] reject implausible region {2!r} {3},{4}".format(idx, total, s["name"], c[0], c[1]), file=sys.stderr, flush=True)
        if not enable_external:
            continue
        region_name = REGION_KW.get(region_code, "")
        vb = region_viewbox(region_code)
        variants = []
        if hint and hint.strip():
            base = hint.strip()
            if region_name:
                variants.append(f"{base}, {region_name}, Россия")
            else:
                variants.append(f"{base}, Россия")
            variants.append(f"автовокзал {base}, {region_name}, Россия" if region_name else f"автовокзал {base}, Россия")
            variants.append(f"автостанция {base}, {region_name}, Россия" if region_name else f"автостанция {base}, Россия")
        else:
            clean = re.sub(r'[«»"\'\(\)]+', ' ', s["name"])
            clean = " ".join(clean.split())
            low_clean = clean.lower()
            toks_clean = low_clean.split()
            is_generic_clean = low_clean in GENERIC_HINTS or low_clean in ("ас автостанция", "ас", "оп", "оп лпк", "ав центральный", "ас «автостанция»") or any(t in GENERIC_HINTS for t in toks_clean)
            if clean and len(clean) >= 4 and not is_generic_clean:
                if region_name:
                    variants.append(f"{clean}, {region_name}, Россия")
                else:
                    variants.append(f"{clean}, Россия")
        yandex_first = hstem and hstem in CITY_OVERRIDE and CITY_OVERRIDE[hstem][0] == "yandex"
        got = None
        for q in variants:
            print("geocode: [{0}/{1}] try {2!r} hint={3!r} region={4}".format(idx, total, q, hint, region_code), file=sys.stderr, flush=True)
            if yandex_first:
                got = yc.lookup(q, region_code=region_code)
                print("geocode: [{0}/{1}] yandex result {2}".format(idx, total, got), file=sys.stderr, flush=True)
                if got and plausible(*got) and region_plausible(got[0], got[1], region_code):
                    s["lat"] = got[0]; s["lon"] = got[1]; found += 1; y_used += 1
                    print("geocode: [{0}/{1}] yandex hit {2!r} -> {3:.4f},{4:.4f} (found {5})".format(idx, total, q, got[0], got[1], found), file=sys.stderr, flush=True)
                    break
                elif got:
                    print("geocode: [{0}/{1}] yandex implausible {2!r} -> {3},{4}".format(idx, total, q, got[0], got[1]), file=sys.stderr, flush=True)
                    got = None
                got = nc.lookup(q, viewbox=vb, region_code=region_code)
                print("geocode: [{0}/{1}] nominatim result {2}".format(idx, total, got), file=sys.stderr, flush=True)
                if got and plausible(*got) and region_plausible(got[0], got[1], region_code):
                    s["lat"] = got[0]; s["lon"] = got[1]; found += 1; n_used += 1
                    print("geocode: [{0}/{1}] nominatim hit {2!r} -> {3:.4f},{4:.4f} (found {5})".format(idx, total, q, got[0], got[1], found), file=sys.stderr, flush=True)
                    break
                elif got:
                    print("geocode: [{0}/{1}] nominatim implausible {2!r} -> {3},{4}".format(idx, total, q, got[0], got[1]), file=sys.stderr, flush=True)
                    got = None
            else:
                got = nc.lookup(q, viewbox=vb, region_code=region_code)
                print("geocode: [{0}/{1}] nominatim result {2}".format(idx, total, got), file=sys.stderr, flush=True)
                if got and plausible(*got) and region_plausible(got[0], got[1], region_code):
                    s["lat"] = got[0]; s["lon"] = got[1]; found += 1; n_used += 1
                    print("geocode: [{0}/{1}] nominatim hit {2!r} -> {3:.4f},{4:.4f} (found {5})".format(idx, total, q, got[0], got[1], found), file=sys.stderr, flush=True)
                    break
                elif got:
                    print("geocode: [{0}/{1}] nominatim implausible {2!r} -> {3},{4}".format(idx, total, q, got[0], got[1]), file=sys.stderr, flush=True)
                    got = None
                got = yc.lookup(q, region_code=region_code)
                print("geocode: [{0}/{1}] yandex result {2}".format(idx, total, got), file=sys.stderr, flush=True)
                if got and plausible(*got) and region_plausible(got[0], got[1], region_code):
                    s["lat"] = got[0]; s["lon"] = got[1]; found += 1; y_used += 1
                    print("geocode: [{0}/{1}] yandex hit {2!r} -> {3:.4f},{4:.4f} (found {5})".format(idx, total, q, got[0], got[1], found), file=sys.stderr, flush=True)
                    break
                elif got:
                    print("geocode: [{0}/{1}] yandex implausible {2!r} -> {3},{4}".format(idx, total, q, got[0], got[1]), file=sys.stderr, flush=True)
                    got = None
        if got is not None:
            continue
        if variants:
            print("geocode: [{0}/{1}] miss {2!r}".format(idx, total, variants[0]), file=sys.stderr, flush=True)
        if idx % 100 == 0 or idx == total:
            elapsed = _gt.time() - _geocode_t0
            print("geocode: прогресс {0}/{1} found={2} n={3} y={4} elapsed={5:.1f}s rate={6:.2f}/s".format(idx, total, found, n_used, y_used, elapsed, idx/max(elapsed, 0.1)), file=sys.stderr, flush=True)
            yc.save(); nc.save()
            print("geocode: кэш сохранён n={0} y={1}".format(len(nc.data), len(yc.data)), file=sys.stderr, flush=True)
    yc.save()
    nc.save()
    missing = [s for s in stops if not s.get("lat") or not s.get("lon") or s.get("lat") == 0 or s.get("lon") == 0]
    if missing:
        miss_path = "data/reestr/missing_stops.json"
        try:
            os.makedirs(os.path.dirname(miss_path), exist_ok=True)
            with open(miss_path, "w", encoding="utf-8") as mf:
                json.dump(missing, mf, ensure_ascii=False, indent=2)
            print("geocode: без координат {0} → {1}".format(len(missing), miss_path), file=sys.stderr, flush=True)
            for s in missing[:20]:
                print("  missing: {0!r} region={1} id={2}".format(s.get("name"), s.get("region"), s.get("id")), file=sys.stderr, flush=True)
            if len(missing) > 20:
                print("  ... и ещё {0}".format(len(missing)-20), file=sys.stderr, flush=True)
        except OSError as e:
            print("geocode: не удалось записать missing {0}: {1}".format(miss_path, e), file=sys.stderr, flush=True)
    print("geocode: координат получено {0} из {1} (nominatim={2}, yandex={3}, used y/n {4}/{5}) elapsed={6:.1f}s".format(found, len(stops), n_used, y_used, yc.used, nc.used, _gt.time()-_geocode_t0),
          file=sys.stderr, flush=True)
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
    import time as _mt
    _main_t0 = _mt.time()
    print("main: старт xlsx={0} regions={1} snapshot={2} osm={3}".format(args.xlsx, args.regions, args.snapshot, args.osm), file=sys.stderr, flush=True)

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
    carriers_map = {}
    for d in sheet_rows(args.xlsx, "sheet3"):
        reg = d.get(ROUTE, "")
        if not reg or reg.startswith("Регистрационный номер"):
            continue
        perevoz[reg] = d
        inn = str(d.get(3, "")).strip()
        if inn and inn not in carriers_map:
            carriers_map[inn] = {
                "name": d.get(2, "").strip(),
                "inn": inn,
                "ogrn": str(d.get(4, "")).strip(),
                "address": d.get(5, "").strip() if d.get(5) else "",
                "email": d.get(6, "").strip() if d.get(6) else "",
            }

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
            if opreg and opreg.strip():
                sid = "op:{0}:{1}".format(region, opreg.strip())
                code = opreg.strip()
            else:
                h = op_hash(name, region)
                slug = translit(name)
                sid = "nr:{0}-{1}".format(slug, h)
                code = "op:{0}:{1}".format(region, h)
            stops[key] = {"id": sid, "name": name, "region": region, "op_reg": code}
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
            row["carrier_inn"] = str(pv.get(3, "")).strip()
            row["carrier_ogrn"] = str(pv.get(4, "")).strip()
            row["carrier_address"] = pv.get(5, "").strip() if pv.get(5) else ""
            row["carrier_email"] = pv.get(6, "").strip() if pv.get(6) else ""
        routes_out.append(row)
    carriers_out = list(carriers_map.values())

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
        "carriers": carriers_out,
        "schedules": sched_out,
    }

    blob = json.dumps(result, ensure_ascii=False, indent=1)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as f:
            f.write(blob)
    else:
        print(blob)

    print("маршрутов touched: {0}, остановок: {1}, блоков расписания: {2} elapsed={3:.1f}s".format(
        len(touched), len(stops_out), len(sched_out), _mt.time()-_main_t0), file=sys.stderr, flush=True)


if __name__ == "__main__":
    main()