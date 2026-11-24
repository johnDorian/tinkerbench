use <workbench.scad>
$fn = 48;

// ================= measured parts (check against yours) =================
bat_l = 48;      // 3xAAA open holder, length (X)   -- measure
bat_w = 36;      // width (Y)                       -- measure
bat_h = 14;      // height incl. cells (Z)          -- measure
esp_l = 22.5;    // ESP32-C3 SuperMini board length (Y, USB end at front)
esp_w = 18;      // board width (X)
esp_pcb = 1.0;   // board thickness
usb_overhang = 0.8; // USB-C shell sticks out past the board edge
usb_zc = 1.6;    // USB-C centre above board top
dht_w = 15.5;    // DHT11 body width
dht_t = 5.5;     // DHT11 body thickness
sw_body_h = 4.5; // slide switch body height (sits on the floor)

// ================= openings =================
usb_w = 12.5;    // opening for the cable's overmould
usb_h = 7;
usb_chamfer = 2.2; // 45 deg top corners keep the bridge short
sw_slot_w = 7;   // lever travel + lever width + margin
sw_slot_h = 3;
vent_w = 2;
vent_h = 10;
vent_pitch = 4;

// ================= shell =================
wall = 2;
floor_t = 2;
lid_t = 2;
fit = 0.3;       // clearance between moving / inserted parts
bat_fit = 0.5;
rib_t = 1.2;     // locating ribs / frames
rib_h = 3;       // battery ribs above the floor
esp_frame_h = 2.5;
dht_cup_h = 6;
head_room = 5;   // above the battery holder: wires + lid lip
sc_l = 14;       // sensor chamber length (X)
div_t = 2;       // divider wall
notch_w = 6;     // wire notch in the divider
notch_d = 5;
lip_h = 4;
lip_t = 1.2;
snap_d = 0.6;    // snap bump height (interference = snap_d - fit)
snap_len = 20;
lid_gap = 10;    // distance between base and lid on the bed

// ================= derived =================
zone_bat = bat_l + 2*bat_fit + 2*rib_t;
zone_esp = esp_w + 2*fit + 2*rib_t + 2;
in_x = zone_bat + zone_esp + div_t + sc_l;
in_y = max(bat_w + 2*bat_fit + 2*rib_t, esp_l + usb_overhang + fit + rib_t + 15);
in_h = bat_h + head_room;
out_x = in_x + 2*wall;
out_y = in_y + 2*wall;
out_h = floor_t + in_h;

bx0 = wall;                 // battery zone start
ex0 = bx0 + zone_bat;       // ESP zone start
dx0 = ex0 + zone_esp;       // divider
sx0 = dx0 + div_t;          // sensor chamber start
cy  = wall + in_y/2;

esp_x0 = ex0 + (zone_esp - esp_w)/2;     // board footprint
esp_y0 = wall + usb_overhang;
usb_x = esp_x0 + esp_w/2;
usb_z = floor_t + esp_pcb + usb_zc;
sw_x  = ex0 + zone_esp/2;
sw_z  = floor_t + sw_body_h/2;
div_top = floor_t + in_h - lip_h - 0.5;
groove_z = out_h - lip_h/2;
snap_xs = [out_x*0.25, out_x*0.75];
ly = out_y + lid_gap;       // lid position on the bed

// ================= fit + printability checks =================
assert(wall >= 0.8 && lip_t >= 0.8 && rib_t >= 0.8, "walls under 2 perimeters");
assert(usb_z - usb_h/2 > 0, "USB opening reaches below the bed");
echo(usb_bridge = usb_w - 2*usb_chamfer);
assert(usb_w - 2*usb_chamfer <= 10, "USB opening: flat top bridge > 10 mm");
assert(sw_slot_w <= 10, "switch slot bridge > 10 mm");
assert(vent_w <= 10, "vent bridge > 10 mm");
echo(lip_bottom = out_h - lip_h, battery_top = floor_t + bat_h, divider_top = div_top);
assert(div_top < out_h - lip_h, "divider hits the lid lip");
assert(floor_t + bat_h < out_h - lip_h, "battery holder hits the lid lip");
// lid (print orientation): lip grows from the plate, bump grows from the lip
echo(lip_into_plate = 0.5, bump_into_lip = snap_d);
assert(0.5 >= 0.4, "lip not fused to the plate");
assert(snap_d >= 0.4 && snap_d <= lip_t - 0.4, "snap bump not fused to / cutting through the lip");
echo(snap_interference = snap_d - fit);
assert(groove_z - (snap_d + 0.15) > floor_t + vent_h + 2, "snap groove runs into the wall vents");
echo(outside_mm = [out_x, out_y, out_h + lid_t]);

// ================= helpers =================
module frame(sx, sy, h, t) {     // open rectangular frame standing on z=0
    difference() {
        cube([sx, sy, h]);
        translate([t, t, floor_t]) cube([sx - 2*t, sy - 2*t, h]);
    }
}
module diamond_x(len, d) {      // 45 deg diamond cross-section, along X, centred
    rotate([0, 90, 0]) linear_extrude(len, center = true)
        rotate(45) square(d*sqrt(2), center = true);
}

// ================= base (printed upright) =================
module base() difference() {
    union() {
        difference() {
            cube([out_x, out_y, out_h]);
            translate([wall, wall, floor_t]) cube([in_x, in_y, in_h + 1]);
        }
        // battery holder ribs
        fy = bat_w + 2*bat_fit + 2*rib_t;
        translate([bx0, cy - fy/2, 0]) frame(zone_bat, fy, floor_t + rib_h, rib_t);
        // board cradle: side rails + back rail, open towards the USB wall
        translate([esp_x0 - fit - rib_t, wall, 0]) difference() {
            cube([esp_w + 2*fit + 2*rib_t, usb_overhang + esp_l + fit + rib_t, floor_t + esp_frame_h]);
            translate([rib_t, -1, floor_t]) cube([esp_w + 2*fit, usb_overhang + esp_l + fit + 1, esp_frame_h + 1]);
        }
        // divider
        difference() {
            translate([dx0, wall - 0.5, 0]) cube([div_t, in_y + 1, div_top]);
            translate([dx0 - 1, cy - notch_w/2, div_top - notch_d]) cube([div_t + 2, notch_w, notch_d + 1]);
        }
        // DHT11 cup (sensor stands pins-up, grille facing the end wall)
        cx = sx0 + sc_l/2;
        translate([cx - (dht_t + fit)/2 - rib_t, cy - (dht_w + fit)/2 - rib_t, 0])
            frame(dht_t + fit + 2*rib_t, dht_w + fit + 2*rib_t, floor_t + dht_cup_h, rib_t);
    }
    // USB-C opening, front wall
    translate([usb_x, wall + 1, usb_z]) rotate([90, 0, 0]) linear_extrude(wall + 2)
        polygon([[-usb_w/2, -usb_h/2], [usb_w/2, -usb_h/2], [usb_w/2, usb_h/2 - usb_chamfer],
                 [usb_w/2 - usb_chamfer, usb_h/2], [-usb_w/2 + usb_chamfer, usb_h/2],
                 [-usb_w/2, usb_h/2 - usb_chamfer]]);
    // switch lever slot, back wall
    translate([sw_x - sw_slot_w/2, out_y - wall - 1, sw_z - sw_slot_h/2]) cube([sw_slot_w, wall + 2, sw_slot_h]);
    // sensor chamber vents: front + back walls
    nv = floor((sc_l - 2) / vent_pitch);
    for (i = [0 : nv - 1], y = [-1, out_y - wall - 1])
        translate([sx0 + 2 + i*vent_pitch, y, floor_t + 2]) cube([vent_w, wall + 2, vent_h]);
    // end wall vents
    ne = floor((in_y - 4) / vent_pitch);
    for (i = [0 : ne - 1])
        translate([out_x - wall - 1, cy - (ne - 1)*vent_pitch/2 + i*vent_pitch - vent_w/2, floor_t + 2])
            cube([wall + 2, vent_w, vent_h]);
    // floor vents beside the cup
    for (y = [wall + 2, wall + 6, wall + in_y - 4, wall + in_y - 8])
        translate([sx0 + 2, y, -1]) cube([sc_l - 4, vent_w, floor_t + 2]);
    // snap grooves, inner faces of the long walls
    for (x = snap_xs, y = [wall, wall + in_y])
        translate([x, y, groove_z]) diamond_x(snap_len + 1, snap_d + 0.15);
}

// ================= lid (printed top face down) =================
module lid() translate([0, ly, 0]) difference() {
    union() {
        cube([out_x, out_y, lid_t]);
        translate([wall + fit, wall + fit, lid_t - 0.5]) difference() {
            cube([in_x - 2*fit, in_y - 2*fit, lip_h + 0.5]);
            translate([lip_t, lip_t, -1]) cube([in_x - 2*fit - 2*lip_t, in_y - 2*fit - 2*lip_t, lip_h + 2]);
        }
        // snap bumps on the lip's outer faces (lid is flipped about X when fitted)
        for (x = snap_xs, y = [wall + fit, wall + in_y - fit])
            translate([x, y, lid_t + lip_h/2]) diamond_x(snap_len, snap_d);
    }
    // vents over the sensor chamber
    for (i = [0 : 2])
        translate([sx0 + 2 + i*vent_pitch, wall + 6, -1]) cube([vent_w, in_y - 12, lid_t + 2]);
}

part("base") base();
part("lid") lid();

feature("USB-C opening", [usb_x, 0, usb_z]);
feature("switch slot", [sw_x, out_y, sw_z]);
feature("DHT11 cup", [sx0 + sc_l/2, cy, floor_t]);
feature("wire notch", [dx0 + div_t/2, cy, div_top - notch_d]);
