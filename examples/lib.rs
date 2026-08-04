// examples/lib.rs — rust as an outline: braces are structure, not text
// This is a comment node. Closing braces never become nodes: the tree IS
// the block structure, and `}` lines regenerate on save.
// TODO implement Display for Vec2 — a real todo node, checkable in the editor.

use std::fmt;

pub struct Vec2 {
    x: f64,
    y: f64,
}

impl Vec2 {
    pub fn new(x: f64, y: f64) -> Self {
        Vec2 { x, y }
    }

    pub fn norm(&self) -> f64 {
        if self.x == 0.0 && self.y == 0.0 {
            return 0.0;
        }
        (self.x * self.x + self.y * self.y).sqrt()
    }
}

enum Shape {
    Circle(f64),
    Rect(f64, f64),
}

fn area(s: &Shape) -> f64 {
    match s {
        Shape::Circle(r) => std::f64::consts::PI * r * r,
        Shape::Rect(w, h) => w * h,
    }
}

fn main() {
    // The next line came from a math node — compose `^ (π, 2.0)` as a tree
    // in the editor, save, and the codec writes real rust: `^` becomes
    // .powf and π its constant.
    let circle = (std::f64::consts::PI).powf(2.0);
    let v = Vec2::new(3.0, 4.0);
    println!("norm = {}", v.norm());
    println!("area = {} vs {}", area(&Shape::Circle(1.0)), circle);
}
