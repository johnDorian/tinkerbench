# Examples

Three worked examples, each built by an agent through the workbenches' MCP servers. Every folder has a README, the `.scad` / `.fzz` files and screenshots of the result.

| Example | Fritzing | OpenSCAD | OrcaSlicer |
|---|:-:|:-:|:-:|
| [ESP32 temperature sensor](esp32-temperature-sensor/) | ✓ | ✓ | ✓ |
| [Replacement part from a photo](replacement-part/) | | ✓ | ✓ |
| [Arduino Nano with button and NeoPixel ring](nano-neopixel-button/) | ✓ | ✓ | |

- **[ESP32 temperature sensor](esp32-temperature-sensor/)**: a DS18B20 sensor on an ESP32 dev board. It has breadboard, schematic and PCB views; a printed case with standoffs, a USB cutout and vents, checked with the `scad-printability` skill; and a slice ready to print. It covers all three apps.
- **[Replacement part from a photo](replacement-part/)**: a broken part photographed on graph paper. `measure_part` gives the outline in millimetres, and caliper readings fill in the heights and depths a photo can't show (`scad-copy-part` skill).
- **[Arduino Nano with button and NeoPixel ring](nano-neopixel-button/)**: a second, different circuit, with a printed diffuser that fits over the ring.

## Running them yourself

```sh
task demo:up     # starts the apps on localhost:8020/8030/8050 and sets up Claude Code in this folder
```

Restart Claude Code in the repo root, approve the `demo-*` MCP servers, and ask for one of the examples. `task demo:down` stops the demo and removes that setup again.

## Recordings

Each example can be watched in either of two ways:

- **Live recording**: the Claude session and the workbench web UI side by side, recorded with OBS.
- **Timelapse**: rebuilt afterwards from the saved model versions, the sketch files and the session transcript, as screenshots with captions.
