"""Capture reference outputs by running the upstream Python regression tests.

Pass the upstream checkout path; install its Python package in the active environment.
"""
import argparse
import sys
import io
import json
import unittest
from pathlib import Path
from unittest.mock import patch

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('upstream', type=Path, help='Path to the upstream telegramify-markdown checkout')
root = parser.parse_args().upstream.resolve()
sys.path.insert(0, str(root))
sys.path.insert(0, str(root / 'src'))

from telegramify_markdown import converter, mdv2, rich
from telegramify_markdown.config import get_runtime_config
from telegramify_markdown.latex_escape.helper import LatexToUnicodeHelper

fixtures = {key: [] for key in ('convert', 'latex', 'mdv2', 'rich', 'split_rich')}

def record_convert(original):
    def call(markdown, **kwargs):
        text, entities, segments = original(markdown, **kwargs)
        cfg = kwargs.get('config') or get_runtime_config()
        fixtures['convert'].append(dict(markdown=markdown, text=text, entities=[e.to_dict() for e in entities],
            disable_latex=not kwargs.get('latex_escape', True), symbols=vars(cfg.markdown_symbol).copy(), expandable=cfg.cite_expandable))
        return text, entities, segments
    return call

def record_latex(original):
    def call(self, source):
        result = original(self, source)
        fixtures['latex'].append(dict(source=source, result=result))
        return result
    return call

def record_mdv2(original):
    def call(text, entities=None):
        result = original(text, entities)
        fixtures['mdv2'].append(dict(text=text, entities=[e.to_dict() for e in entities or []], result=result))
        return result
    return call

def record_rich(original):
    def call(markdown, **kwargs):
        result = original(markdown, **kwargs)
        fixtures['rich'].append(dict(markdown=markdown, options=kwargs, result=result.to_dict()))
        return result
    return call

def record_split_rich(original):
    def call(message, **kwargs):
        result = original(message, **kwargs)
        fixtures['split_rich'].append(dict(message=message.to_dict(), options=kwargs, result=[r.to_dict() for r in result]))
        return result
    return call

with patch.object(converter, 'convert_with_segments', record_convert(converter.convert_with_segments)), \
     patch.object(LatexToUnicodeHelper, 'convert', record_latex(LatexToUnicodeHelper.convert)), \
     patch.object(mdv2, 'entities_to_markdownv2', record_mdv2(mdv2.entities_to_markdownv2)), \
     patch.object(rich, 'richify', record_rich(rich.richify)), \
     patch.object(rich, 'split_rich', record_split_rich(rich.split_rich)):
    # Patch the public aliases before the tests import them.
    import telegramify_markdown as tm
    tm.richify = rich.richify
    tm.split_rich = rich.split_rich
    tm.entities_to_markdownv2 = mdv2.entities_to_markdownv2
    suite = unittest.TestSuite(unittest.defaultTestLoader.loadTestsFromName('tests.' + name)
        for name in ('test_converter', 'test_mdv2', 'test_rich', 'test_entity', 'test_pipeline'))
    result = unittest.TextTestRunner(stream=io.StringIO()).run(suite)
    if not result.wasSuccessful():
        raise RuntimeError(result.errors + result.failures)

    extra = [
        "hello\n---", "hello\n***", "# head\n---", "- item\n***",
        "# head\n```\ncode\n```", "before\n\n```\n```\n\nafter",
        "|| hidden ||", "a ||x `code` y|| b", "[**bold**](https://example.com)",
        "[😀](tg://emoji?id=5368324170671202286)",
        "\\&amp; &copy; &bogus;", "one\\*two", "[x](https://a.test/a?x=1&amp;y=2)",
        "$$\nx^2\n$$", "x $$a$$ y", "$ spaced $", "$x$2 and $y$", "$10 and $20",
        "- first\n\n  second\n\n  > quoted\n\n- last",
        "note[^key]\n\n[^key]: A **note**.",
        "```\n```\n\n||secret||", "```\n\n```\n\n||secret||",
        "||\n**secret**\n||", "<div>raw</div>\n\nAfter",
    ]
    for markdown in extra:
        converter.convert(markdown)
        rich.richify(markdown)
    for relative in ('tests/exp1.md', 'tests/exp2.md', 'playground/t_text.md', 'playground/t_longtext.md'):
        markdown = (root / relative).read_text()
        converter.convert(markdown)
        rich.richify(markdown)

for key, rows in fixtures.items():
    unique = {json.dumps(row, ensure_ascii=False, sort_keys=True): row for row in rows}
    fixtures[key] = list(unique.values())
    print(key, len(fixtures[key]))
path = Path(__file__).resolve().parents[1] / 'testdata' / 'reference.json'
path.parent.mkdir(exist_ok=True)
path.write_text(json.dumps(fixtures, ensure_ascii=False, indent=2) + '\n')
