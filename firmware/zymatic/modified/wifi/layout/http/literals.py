"""Pack the extended HTTP response literals in a temporary AVR benchmarking tree."""
import ast
from collections import Counter
import re
from pathlib import Path

# All response text is static ASCII. Leave names used to match incoming HTTP unchanged.
QUOTED = r'"(?:[^"\\]|\\.)*"'
SENTINEL = re.compile(r'\bliteral\(n,')
CONSTANT = re.compile(r'CONSTANT\((' + QUOTED + r')\)')


def calls(source):
    for start in SENTINEL.finditer(source):
        pos = start.end()
        level = 1
        while level:
            ch = source[pos]
            if ch in ('"', "'"):
                quote = ch
                pos += 1
                while source[pos] != quote:
                    pos += 2 if source[pos] == '\\' else 1
            elif ch == '(':
                level += 1
            elif ch == ')':
                level -= 1
            pos += 1
        yield start.start(), pos


def encode(texts):
    tokens = {value: value for value in texts}
    pairs = []
    for code in range(128, 256):
        counts = Counter(pair for value in tokens.values() for pair in zip(value, value[1:]))
        if not counts:
            break
        best, frequency = counts.most_common(1)[0]
        if frequency <= 2:
            break
        pairs.append(best)
        for text, value in tokens.items():
            tokens[text] = value.replace(bytes(best), bytes([code]))
    return tokens, pairs


def stage(runtime):
    path = Path(runtime) / 'zymatic_api.c'
    source = path.read_text()
    bounds = list(calls(source))
    def decode(value):
        return ast.literal_eval(value).encode('ascii')
    texts = dict.fromkeys(decode(match[1]) for begin, end in bounds
                          for match in CONSTANT.finditer(source[begin:end]))
    encoded, pairs = encode(texts)
    def replace(match):
        # Three-digit octal always terminates at a byte boundary in a C string.
        escaped = ''.join('\\%03o' % code for code in encoded[decode(match[1])])
        return 'CONSTANT("' + escaped + '")'
    for begin, end in reversed(bounds):
        source = source[:begin] + CONSTANT.sub(replace, source[begin:end]) + source[end:]
    needle = 'static void literal(ZymaticNetwork *n, FlashAddress address)'
    if source.count(needle) != 1:
        raise ValueError('Expected the extended response writer')
    dictionary = ','.join(str(byte) for pair in pairs for byte in pair)
    writer = """
static const __flash1 uint8_t http_pairs[] = {DICTIONARY};

static char *append_token(char *out, char *end, uint8_t symbol)
{
    while (symbol >= 128) {
        uint8_t row = (uint8_t)((symbol - 128) * 2);
        uint8_t left = http_pairs[row];
        if (out == end) return out;
        if (left < 128) *out++ = (char)left;
        else out = append_token(out, end, left);
        symbol = http_pairs[row + 1];
    }
    if (out != end) *out++ = (char)symbol;
    return out;
}
"""
    source = source.replace(needle, writer.replace('DICTIONARY', dictionary) + needle)
    old = 'while ((ch = (char)READ(address++)) != 0) append(n, ch);'
    if source.count(old) != 1:
        raise ValueError('Expected one literal output loop')
    fast_writer = """if (n->response_bytes >= sizeof(n->buffer.reply)) return;
    char *out = (char *)n->buffer.reply + n->response_bytes;
    char *end = (char *)n->buffer.reply + sizeof(n->buffer.reply);
    while (out != end && (ch = (char)READ(address++)) != 0) {
        uint8_t symbol = (uint8_t)ch;
        if (symbol < 128) *out++ = ch;
        else out = append_token(out, end, symbol);
    }
    n->response_bytes = (uint16_t)(out - (char *)n->buffer.reply);"""
    path.write_text(source.replace(old, fast_writer))
    # Prove all string round trips with a bounded expansion depth.
    expanded = [bytes([i]) for i in range(128)]
    depths = [0] * 128
    for left, right in pairs:
        expanded.append(expanded[left] + expanded[right])
        depths.append(1 + max(depths[left], depths[right]))
    assert all(b''.join(expanded[i] for i in value) == text for text, value in encoded.items())
    return len(pairs), max(depths), sum(map(len, texts)) - sum(map(len, encoded.values())) - 2*len(pairs)
