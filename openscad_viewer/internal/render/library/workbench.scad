// workbench.scad: named parts and features for the OpenSCAD workbench.
//
//   use <workbench.scad>
//
// part("name") { ... }  marks one physical piece. The workbench renders the model once for
//   every name (out/parts/<n>.stl) and once more for the geometry outside any part, so the
//   web UI can show, select and comment on "the lid". Put every piece in a part, including
//   the plain geometry of a model that has only one piece. Parts can be used as the first
//   child of difference(), but not as subtracted children: a subtracted part is rendered
//   without the body it cuts, so its mesh would be wrong.
//   Nested part() calls are not supported: the inner one belongs to the outer part, is ignored
//   and a warning is echoed. Names "__rest__" and "(unlabelled)" are reserved; such a part
//   renders as plain geometry.
//
// feature("name", at) renders nothing. It names a point (holes, chamfers, mounting points)
//   in ABSOLUTE model coordinates [x, y, z] in mm: OpenSCAD can't report the current transform,
//   so compute `at` yourself. Features are listed in the viewer and in list_parts.
//
// Filtering: the workbench writes a wrapper file that sets $only_part and includes main.scad,
// so no OpenSCAD command line flag for $ variables is needed. Unset $only_part renders
// everything; $only_part == name renders that part only; any other value renders no part.

module part(name) {
  if (name == "__rest__" || name == "(unlabelled)") {
    echo(str("WORKBENCH WARNING: part(\"", name, "\") is a reserved name; rendered as plain geometry"));
    children();
  } else if (is_undef($wb_part)) {
    __wb_part(name) children();
  } else {
    __wb_nested(name, $wb_part) children();
  }
}

module __wb_part(name) {
  echo(str("PART:", name));
  if (is_undef($only_part) || $only_part == name) {
    $wb_part = name;
    children();
  }
}

module __wb_nested(name, outer) {
  echo(str("WORKBENCH WARNING: part(\"", name, "\") is inside part(\"", outer, "\"); it belongs to that part and is ignored"));
  children();
}

module feature(name, at) {
  echo(str("FEATURE:", name, ":", at));
}
