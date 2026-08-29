# Fowler smell baseline

Apply these as labelled judgement calls, never hard violations. Repository standards override this baseline, and tooling-enforced findings are omitted.

- **Mysterious Name** — a name does not reveal what it does or holds. Rename it; if no honest name comes, the design is murky.
- **Duplicated Code** — the same logic shape appears in more than one hunk or file. Extract the shared shape.
- **Feature Envy** — a method reaches into another object's data more than its own. Move the method onto the data it envies.
- **Data Clumps** — the same fields or parameters keep travelling together. Bundle them into a type.
- **Primitive Obsession** — a primitive stands in for a domain concept. Give the concept a small type.
- **Repeated Switches** — the same conditional dispatch recurs for one type. Use polymorphism or one shared map.
- **Shotgun Surgery** — one logical change forces scattered edits. Gather what changes together.
- **Divergent Change** — one module changes for unrelated reasons. Split it by reason.
- **Speculative Generality** — abstractions or hooks serve no current requirement. Delete or inline them.
- **Message Chains** — callers navigate long object chains. Hide the walk behind the first object.
- **Middle Man** — a type or function mainly delegates. Call the real target directly.
- **Refused Bequest** — an implementer ignores most inherited behavior. Prefer composition.
