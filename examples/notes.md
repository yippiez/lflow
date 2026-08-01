# Trip planner — every markdown construct as nodes

This opening line is a paragraph: in the editor it is a text node (¶ glyph), and it saves back as prose — markdown is not forced into an outline. Wrapped source lines join into one paragraph, exactly like markdown itself.

<!-- this line is a comment node: invisible on a rendered markdown page -->

## Packing list

Lists are where the outline shines — each item is a node, nesting is real:

- [ ] passports
- [x] book the ferry
- clothing
  - rain jacket
  - two pairs of boots

> The sea does not reward those who are too anxious. — a quote node

## Route math

The `$$` block below is a math node: retype any node to math in the editor and compose the expression AS an outline; it saves back in this linear form.

$$
(distance ÷ speed)
$$

## Ferry timetable

The grid is a table node — columns are its children, cells are theirs. alt+e in the editor opens the grid face:

| port | departs | fare |
| --- | --- | --- |
| Hirtshals | 08:15 | 420 |
| Kristiansand | 12:40 | 380 |

## Scripts

Fenced code keeps its language tag (it lives on the node's note), so this reopens as a code node that still knows it is bash:

```bash
curl -s "https://api.ferries.example/v1/departures?port=hirtshals" | jq '.next'
```

<!-- nlp: sum the fares column of the timetable above -->

---

### Fine print

Everything under a heading nests under that heading's node — zoom into "Ferry timetable" in the editor and the grid plus its scripts are the whole view. Closing prose, like this line, is again just a paragraph.
