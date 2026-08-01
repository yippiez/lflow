# examples/app.py — python as an outline, annotated by its own constructs
# A plain comment line like this one is a comment node (dim ◦ row).
# TODO teach the fetcher to retry — this line is a real todo node: check it
# in the editor and it saves back as `# DONE …`.

import json
import math
import urllib.request


def fetch_rates(url):
    # `def fetch_rates(url):` is a fn node — in the editor the row reads
    # `ƒ fetch_rates(url)` and this body is its children; folding the header
    # shows a dim `· n lines` tail instead of the code.
    with urllib.request.urlopen(url) as resp:
        return json.load(resp)


def convert(amount, rate):
    if rate <= 0:
        raise ValueError("rate must be positive")
    return amount * rate


class Wallet:
    # `class Wallet:` is a class node (◇) — methods are fn nodes inside it,
    # so the outline reads: class > fn > statements.
    def __init__(self, balance):
        self.balance = balance

    def spend(self, amount):
        if amount > self.balance:
            raise ValueError("insufficient funds")
        self.balance -= amount
        return self.balance


# The line below came from a MATH node: compose `= / compound (principal ×
# (1 + rate) ^ years)` as an outline in the editor, save, and the codec
# writes the real expression — π becomes math.pi, ^ becomes **:
compound = principal * (1 + rate) ** years

# nlp: read rates.json and print the three strongest currencies
# (the line above is an nlpcompute node: its instruction rides as this
# comment and its generated code, once you alt+r it, lands right below)

# lflow query: type:todo
# (a foreign node type — here a saved query — travels as a marker comment
# and is restored as its real node when the file reopens)


if __name__ == "__main__":
    wallet = Wallet(100)
    print(wallet.spend(30))
