-- Seed vocabulary (FRONTEND.md, topic reseed): NeetCode 150's 18
-- categories kept verbatim, plus 17 LeetCode tags for concepts NC150
-- has no category for at all (not finer subdivisions of something NC150
-- already groups together — e.g. no separate "Array"/"Hash Table" since
-- NC150's "Arrays & Hashing" already covers both).
-- New tags can still be created freely later — this is a starting point,
-- not a fixed list.
--
-- Run exactly ONCE, only when the topics table is empty (see db.go's
-- Open) — NOT on every startup like schema.sql. A plain INSERT (not
-- INSERT OR IGNORE) is deliberate: Open already guarantees this only
-- runs against an empty table, so there's nothing to ignore, and a
-- straight INSERT fails loudly if that guarantee is ever violated
-- instead of silently doing nothing.
INSERT INTO topics (name) VALUES
    -- NeetCode 150
    ('Arrays & Hashing'),
    ('Two Pointers'),
    ('Sliding Window'),
    ('Stack'),
    ('Binary Search'),
    ('Linked List'),
    ('Trees'),
    ('Tries'),
    ('Heap / Priority Queue'),
    ('Backtracking'),
    ('Graphs'),
    ('Advanced Graphs'),
    ('1-D Dynamic Programming'),
    ('2-D Dynamic Programming'),
    ('Greedy'),
    ('Intervals'),
    ('Math & Geometry'),
    ('Bit Manipulation'),
    -- LeetCode gap-fill
    ('String'),
    ('Doubly-Linked List'),
    ('Queue'),
    ('Prefix Sum'),
    ('Monotonic Stack'),
    ('Recursion'),
    ('Divide and Conquer'),
    ('Enumeration'),
    ('Simulation'),
    ('Sorting'),
    ('Memoization'),
    ('Union-Find'),
    ('Depth-First Search'),
    ('Breadth-First Search'),
    ('Counting'),
    ('Database'),
    ('Design');
