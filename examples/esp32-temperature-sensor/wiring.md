# esp32c3-dht11-sensor: wiring

## Purpose
Battery-powered temperature/humidity sensor: ESP32-C3 SuperMini + DHT11. Wakes from deep sleep, powers the DHT11, reads it, sends the reading over Wi-Fi, sleeps again.

## Power: 3 × AAA (4.5 V nominal)
- 3 × AAA in series → slide switch SW1 → SuperMini **5V** pin → onboard 3.3 V LDO (ME6211, ~0.1–0.25 V dropout).
- Why 3 cells: 2 × AAA (3.0 V falling to 2.4 V) is below what the ESP32-C3 needs (3.0 V min) once the cells sag; 4 × AAA (6.0–6.4 V fresh) is near the LDO's 6.5 V limit and only burns the extra volt as heat. 3 cells run the LDO down to ~1.15 V/cell (≈3.45 V), which uses most of an alkaline cell's capacity.
- The battery holder art is Fritzing's 2 × AA pack (no 3 × AAA part exists); the real part is a 3 × AAA holder (e.g. 3xAAA box with switch, ~48 × 35 × 14 mm).
- Rough battery life (alkaline AAA ≈ 1000 mAh): wake every 15 min, ~1.5 s awake with Wi-Fi (static IP, no scan) → ~0.25 mAh/h incl. sleep current → about 5–6 months. With a 10 min interval and 4 s DHCP connects: ~2 months.

## Pins (firmware)
| Function | Pin | Notes |
|---|---|---|
| DHT11 power (VDD) | GPIO4 | Drive HIGH ~1.5 s before reading (DHT11 needs ~1 s after power-up), LOW before deep sleep. Saves the DHT11's ~150 µA standby. |
| DHT11 data | GPIO3 | 10 kΩ pull-up R1 to GPIO4 (so the pull-up is unpowered in sleep too). |
| Battery sense | GPIO1 (ADC1_CH1) | VBAT/2 through R2/R3 100 k/100 k. Use 12 dB attenuation; VBAT = 2 × reading. Fresh 4.8 V → 2.4 V at the pin. Go to permanent sleep below ~3.5 V. |
Strapping pins GPIO2, GPIO8 (onboard LED), GPIO9 (BOOT button) are left free.

## Build steps
1. **Remove (or cut the trace of) the SuperMini's red power LED** – it draws 1–3 mA permanently, which alone would empty the cells in weeks.
2. Solder battery + → switch common (P); switch S → SuperMini 5V pin.
3. Battery − → SuperMini GND.
4. DHT11: VDD → GPIO4, DATA → GPIO3, GND → GND; pin 3 (NC) unconnected. 10 kΩ between DHT VDD and DATA.
5. Divider: 100 kΩ from 5V pin to GPIO1, 100 kΩ from GPIO1 to GND.

## Check before powering up
- If your DHT11 is a 3-pin module (blue board), it already has the pull-up: leave R1 out, and check the module's pin order (often S / + / −).
- Measure 4.2–4.8 V on the 5V pin and 3.3 V on the 3V3 pin with the switch on.
- **Switch the batteries off while USB is plugged in**: the SuperMini's USB 5 V and the 5V pin are tied together (possibly through a diode, depending on the clone), so USB could back-feed the cells.
- The DHT11 is slow and coarse (±2 °C, ±5 % RH, 1 reading/s max). If you want better accuracy later, an SHT31/AHT20 fits the same case on I²C.

## Not done
- PCB view not routed (point-to-point wiring inside the case).
- The SuperMini part is a community part (byTheTV/AirGlove on GitHub) with pin names fixed up for the checks; its PCB footprint is unverified.

## Connections (breadboard view)

| Net | From | To | Wire |
|---|---|---|---|
| GND | BAT1.- (negative side) | DHT1.GND (ground) | black |
| GND | BAT1.- (negative side) | R3.Pin 1 | black |
| GND | BAT1.- (negative side) | U1.GND | black |
| Net 1 | BAT1.+ (positive side) | SW1.P | red |
| Net 2 | DHT1.VDD (supply voltage) | R1.Pin 1 | red |
| Net 2 | DHT1.VDD (supply voltage) | U1.GPIO4 | red |
| Net 3 | DHT1.DATA (data signal) | R1.Pin 0 | orange |
| Net 3 | DHT1.DATA (data signal) | U1.GPIO3 | orange |
| 5 V | R2.Pin 0 | SW1.S | red |
| 5 V | R2.Pin 0 | U1.5V | red |
| Net 4 | R2.Pin 1 | R3.Pin 0 | blue |
| Net 4 | R2.Pin 1 | U1.GPIO1 | blue |

Layout: 12 wires, 435 mm of wire, 4 crossings.

## Pins in use

**U1**

- GPIO3, net Net 3 → DHT1.DATA (data signal), R1.Pin 0
- GPIO4, net Net 2 → DHT1.VDD (supply voltage), R1.Pin 1
- GND, net GND → BAT1.- (negative side), DHT1.GND (ground), R3.Pin 1
- 5V, net 5 V → R2.Pin 0, SW1.S
- GPIO1, net Net 4 → R2.Pin 1, R3.Pin 0

**DHT1**

- VDD (supply voltage), net Net 2 → R1.Pin 1, U1.GPIO4
- DATA (data signal), net Net 3 → R1.Pin 0, U1.GPIO3
- GND (ground), net GND → BAT1.- (negative side), R3.Pin 1, U1.GND

**BAT1**

- - (negative side), net GND → DHT1.GND (ground), R3.Pin 1, U1.GND
- + (positive side), net Net 1 → SW1.P

**SW1**

- P, net Net 1 → BAT1.+ (positive side)
- S, net 5 V → R2.Pin 0, U1.5V

**R1**

- Pin 0, net Net 3 → DHT1.DATA (data signal), U1.GPIO3
- Pin 1, net Net 2 → DHT1.VDD (supply voltage), U1.GPIO4

**R2**

- Pin 0, net 5 V → SW1.S, U1.5V
- Pin 1, net Net 4 → R3.Pin 0, U1.GPIO1

**R3**

- Pin 0, net Net 4 → R2.Pin 1, U1.GPIO1
- Pin 1, net GND → BAT1.- (negative side), DHT1.GND (ground), U1.GND

## Parts

| Qty | Part | Value | Package | Titles |
|---|---|---|---|---|
| 1 | 220 Ω Resistor | 10 kΩ |  | R1 |
| 2 | 220 Ω Resistor | 100 kΩ |  | R2, R3 |
| 1 | Battery | 4.5V |  | BAT1 |
| 1 | DHT11 Humitidy and Temperature Sensor |  |  | DHT1 |
| 1 | Esp32-c3 super mini |  |  | U1 |
| 1 | SPST Switch |  |  | SW1 |
