//! Structure-of-Arrays (SoA) B-Tree for high-performance compute lanes.
//! Nodes store packed 8-byte inline prefixes in contiguous arrays.
//! Linear scanning over packed prefixes minimizes branch mispredictions and cache misses.

use std::fmt::Debug;

pub const SOA_NODE_CAPACITY: usize = 16;

/// InlineKey extracts an 8-byte big-endian prefix for fast comparisons.
pub trait InlineKey: Ord + Clone + Debug {
    fn inline_prefix(&self) -> u64;
}

impl InlineKey for String {
    #[inline]
    fn inline_prefix(&self) -> u64 {
        let bytes = self.as_bytes();
        let mut buffer = [0u8; 8];
        let len = bytes.len().min(8);
        buffer[..len].copy_from_slice(&bytes[..len]);
        u64::from_be_bytes(buffer)
    }
}

impl InlineKey for &str {
    #[inline]
    fn inline_prefix(&self) -> u64 {
        let bytes = self.as_bytes();
        let mut buffer = [0u8; 8];
        let len = bytes.len().min(8);
        buffer[..len].copy_from_slice(&bytes[..len]);
        u64::from_be_bytes(buffer)
    }
}

impl InlineKey for u64 {
    #[inline]
    fn inline_prefix(&self) -> u64 {
        *self
    }
}

impl InlineKey for i64 {
    #[inline]
    fn inline_prefix(&self) -> u64 {
        // Toggle sign bit for correct two's complement ordering in unsigned space
        (*self as u64) ^ 0x8000_0000_0000_0000
    }
}

#[derive(Clone, Debug)]
pub struct LeafNode<K, V> {
    pub prefixes: [u64; SOA_NODE_CAPACITY],
    pub keys: Vec<K>,
    pub values: Vec<V>,
}

impl<K: InlineKey, V> Default for LeafNode<K, V> {
    fn default() -> Self {
        Self::new()
    }
}

type LeafSplit<K, V> = (Option<V>, Option<(u64, K, LeafNode<K, V>)>);
type NodeSplit<K, V> = (Option<V>, Option<(u64, K, Node<K, V>)>);

impl<K: InlineKey, V> LeafNode<K, V> {
    pub fn new() -> Self {
        Self {
            prefixes: [0; SOA_NODE_CAPACITY],
            keys: Vec::with_capacity(SOA_NODE_CAPACITY),
            values: Vec::with_capacity(SOA_NODE_CAPACITY),
        }
    }

    #[inline]
    pub fn search(&self, key: &K, prefix: u64) -> Result<usize, usize> {
        let len = self.keys.len();
        let mut idx = 0;
        while idx < len {
            let p = self.prefixes[idx];
            if p > prefix {
                return Err(idx);
            }
            if p == prefix {
                match self.keys[idx].cmp(key) {
                    std::cmp::Ordering::Equal => return Ok(idx),
                    std::cmp::Ordering::Greater => return Err(idx),
                    std::cmp::Ordering::Less => {}
                }
            }
            idx += 1;
        }
        Err(len)
    }

    pub fn insert(&mut self, key: K, value: V) -> LeafSplit<K, V> {
        let prefix = key.inline_prefix();
        match self.search(&key, prefix) {
            Ok(idx) => {
                let old = std::mem::replace(&mut self.values[idx], value);
                (Some(old), None)
            }
            Err(idx) => {
                let len = self.keys.len();
                if len < SOA_NODE_CAPACITY {
                    // Shift prefixes
                    for i in (idx..len).rev() {
                        self.prefixes[i + 1] = self.prefixes[i];
                    }
                    self.prefixes[idx] = prefix;
                    self.keys.insert(idx, key);
                    self.values.insert(idx, value);
                    (None, None)
                } else {
                    // Split leaf
                    let mut right = LeafNode::new();
                    let mid = SOA_NODE_CAPACITY / 2;

                    // Elements from mid..len go to right
                    let right_len = len - mid;
                    right.prefixes[..right_len].copy_from_slice(&self.prefixes[mid..len]);
                    right.keys.extend(self.keys.drain(mid..));
                    right.values.extend(self.values.drain(mid..));

                    // Insert the new key into left or right
                    if idx <= mid {
                        let left_len = self.keys.len();
                        for i in (idx..left_len).rev() {
                            self.prefixes[i + 1] = self.prefixes[i];
                        }
                        self.prefixes[idx] = prefix;
                        self.keys.insert(idx, key);
                        self.values.insert(idx, value);
                    } else {
                        let r_idx = idx - mid;
                        let r_len = right.keys.len();
                        for i in (r_idx..r_len).rev() {
                            right.prefixes[i + 1] = right.prefixes[i];
                        }
                        right.prefixes[r_idx] = prefix;
                        right.keys.insert(r_idx, key);
                        right.values.insert(r_idx, value);
                    }

                    let split_prefix = right.prefixes[0];
                    let split_key = right.keys[0].clone();
                    (None, Some((split_prefix, split_key, right)))
                }
            }
        }
    }
}

#[derive(Clone, Debug)]
pub struct InternalNode<K, V> {
    pub prefixes: [u64; SOA_NODE_CAPACITY],
    pub keys: Vec<K>,
    pub children: Vec<Node<K, V>>,
}

impl<K: InlineKey, V> Default for InternalNode<K, V> {
    fn default() -> Self {
        Self::new()
    }
}

impl<K: InlineKey, V> InternalNode<K, V> {
    pub fn new() -> Self {
        Self {
            prefixes: [0; SOA_NODE_CAPACITY],
            keys: Vec::with_capacity(SOA_NODE_CAPACITY),
            children: Vec::with_capacity(SOA_NODE_CAPACITY + 1),
        }
    }

    #[inline]
    pub fn search_child(&self, key: &K, prefix: u64) -> usize {
        let count = self.keys.len();
        let mut idx = 0;
        while idx < count {
            let p = self.prefixes[idx];
            if prefix < p {
                return idx;
            }
            if prefix == p && key < &self.keys[idx] {
                return idx;
            }
            idx += 1;
        }
        idx
    }

    pub fn insert_child(
        &mut self,
        insert_idx: usize,
        split_prefix: u64,
        split_key: K,
        right_child: Node<K, V>,
    ) -> Option<(u64, K, InternalNode<K, V>)> {
        let count = self.keys.len();
        for i in (insert_idx..count).rev() {
            self.prefixes[i + 1] = self.prefixes[i];
        }
        self.prefixes[insert_idx] = split_prefix;
        self.keys.insert(insert_idx, split_key);
        self.children.insert(insert_idx + 1, right_child);

        if self.keys.len() >= SOA_NODE_CAPACITY {
            let mid = self.keys.len() / 2;
            let promoted_prefix = self.prefixes[mid];
            let promoted_key = self.keys.remove(mid);

            let mut right = InternalNode::new();
            let right_keys: Vec<K> = self.keys.drain(mid..).collect();
            let right_count = right_keys.len();
            for i in 0..right_count {
                right.prefixes[i] = self.prefixes[mid + 1 + i];
            }
            right.keys = right_keys;
            right.children = self.children.drain((mid + 1)..).collect();

            Some((promoted_prefix, promoted_key, right))
        } else {
            None
        }
    }
}

#[derive(Clone, Debug)]
pub enum Node<K, V> {
    Internal(InternalNode<K, V>),
    Leaf(LeafNode<K, V>),
}

impl<K: InlineKey, V> Node<K, V> {
    pub fn get(&self, key: &K, prefix: u64) -> Option<&V> {
        match self {
            Node::Leaf(leaf) => match leaf.search(key, prefix) {
                Ok(idx) => Some(&leaf.values[idx]),
                Err(_) => None,
            },
            Node::Internal(internal) => {
                let child_idx = internal.search_child(key, prefix);
                internal.children[child_idx].get(key, prefix)
            }
        }
    }

    pub fn get_mut(&mut self, key: &K, prefix: u64) -> Option<&mut V> {
        match self {
            Node::Leaf(leaf) => match leaf.search(key, prefix) {
                Ok(idx) => Some(&mut leaf.values[idx]),
                Err(_) => None,
            },
            Node::Internal(internal) => {
                let child_idx = internal.search_child(key, prefix);
                internal.children[child_idx].get_mut(key, prefix)
            }
        }
    }
}

/// SoABTreeMap is an ordered map with Structure-of-Arrays node layout.
/// Prefix arrays are packed into contiguous cache lines for maximum memory bandwidth.
#[derive(Clone, Debug)]
pub struct SoABTreeMap<K, V> {
    root: Node<K, V>,
    len: usize,
}

impl<K: InlineKey, V> Default for SoABTreeMap<K, V> {
    fn default() -> Self {
        Self::new()
    }
}

impl<K: InlineKey, V> SoABTreeMap<K, V> {
    pub fn new() -> Self {
        Self { root: Node::Leaf(LeafNode::new()), len: 0 }
    }

    #[inline]
    pub fn len(&self) -> usize {
        self.len
    }

    #[inline]
    pub fn is_empty(&self) -> bool {
        self.len == 0
    }

    #[inline]
    pub fn get(&self, key: &K) -> Option<&V> {
        let prefix = key.inline_prefix();
        self.root.get(key, prefix)
    }

    #[inline]
    pub fn get_mut(&mut self, key: &K) -> Option<&mut V> {
        let prefix = key.inline_prefix();
        self.root.get_mut(key, prefix)
    }

    #[inline]
    pub fn contains_key(&self, key: &K) -> bool {
        self.get(key).is_some()
    }

    pub fn insert(&mut self, key: K, value: V) -> Option<V> {
        let prefix = key.inline_prefix();
        let (replaced, split) = insert_recursive(&mut self.root, key, value, prefix);
        if replaced.is_none() {
            self.len += 1;
        }

        if let Some((split_prefix, split_key, right_child)) = split {
            let mut new_root = InternalNode::new();
            let old_root = std::mem::replace(&mut self.root, Node::Leaf(LeafNode::new()));
            new_root.prefixes[0] = split_prefix;
            new_root.keys.push(split_key);
            new_root.children.push(old_root);
            new_root.children.push(right_child);
            self.root = Node::Internal(new_root);
        }

        replaced
    }
}

fn insert_recursive<K: InlineKey, V>(
    node: &mut Node<K, V>,
    key: K,
    value: V,
    prefix: u64,
) -> NodeSplit<K, V> {
    match node {
        Node::Leaf(leaf) => {
            let (replaced, split) = leaf.insert(key, value);
            (replaced, split.map(|(p, k, r)| (p, k, Node::Leaf(r))))
        }
        Node::Internal(internal) => {
            let child_idx = internal.search_child(&key, prefix);
            let (replaced, split) =
                insert_recursive(&mut internal.children[child_idx], key, value, prefix);

            if let Some((split_prefix, split_key, right_child)) = split {
                let parent_split =
                    internal.insert_child(child_idx, split_prefix, split_key, right_child);
                (replaced, parent_split.map(|(p, k, r)| (p, k, Node::Internal(r))))
            } else {
                (replaced, None)
            }
        }
    }
}

pub const NULL_NODE: u32 = u32::MAX;

/// ArenaLeafNode stores up to 16 key-value pairs inline without heap allocation.
#[derive(Clone, Debug)]
pub struct ArenaLeafNode<K, V> {
    pub prefixes: [u64; SOA_NODE_CAPACITY],
    pub keys: [Option<K>; SOA_NODE_CAPACITY],
    pub values: [Option<V>; SOA_NODE_CAPACITY],
    pub len: usize,
}

impl<K, V> Default for ArenaLeafNode<K, V> {
    fn default() -> Self {
        Self::new()
    }
}

impl<K, V> ArenaLeafNode<K, V> {
    pub fn new() -> Self {
        Self {
            prefixes: [0; SOA_NODE_CAPACITY],
            keys: Default::default(),
            values: Default::default(),
            len: 0,
        }
    }

    #[inline]
    pub fn search(&self, key: &K, prefix: u64) -> Result<usize, usize>
    where
        K: InlineKey,
    {
        let mut idx = 0;
        while idx < self.len {
            let p = self.prefixes[idx];
            if p > prefix {
                return Err(idx);
            }
            if p == prefix {
                if let Some(ref k) = self.keys[idx] {
                    match k.cmp(key) {
                        std::cmp::Ordering::Equal => return Ok(idx),
                        std::cmp::Ordering::Greater => return Err(idx),
                        std::cmp::Ordering::Less => {}
                    }
                }
            }
            idx += 1;
        }
        Err(self.len)
    }
}

/// ArenaInternalNode stores up to 16 separator keys and 17 node indices.
#[derive(Clone, Debug)]
pub struct ArenaInternalNode<K> {
    pub prefixes: [u64; SOA_NODE_CAPACITY],
    pub keys: [Option<K>; SOA_NODE_CAPACITY],
    pub children: [u32; SOA_NODE_CAPACITY + 1],
    pub len: usize,
}

impl<K> Default for ArenaInternalNode<K> {
    fn default() -> Self {
        Self::new()
    }
}

impl<K> ArenaInternalNode<K> {
    pub fn new() -> Self {
        Self {
            prefixes: [0; SOA_NODE_CAPACITY],
            keys: Default::default(),
            children: [NULL_NODE; SOA_NODE_CAPACITY + 1],
            len: 0,
        }
    }

    #[inline]
    pub fn search_child(&self, key: &K, prefix: u64) -> usize
    where
        K: InlineKey,
    {
        let mut idx = 0;
        while idx < self.len {
            let p = self.prefixes[idx];
            if prefix < p {
                return idx;
            }
            if prefix == p {
                if let Some(ref k) = self.keys[idx] {
                    if key < k {
                        return idx;
                    }
                }
            }
            idx += 1;
        }
        idx
    }
}

#[derive(Clone, Debug)]
pub enum ArenaNode<K, V> {
    Leaf(ArenaLeafNode<K, V>),
    Internal(ArenaInternalNode<K>),
}

/// ArenaSoABTreeMap allocates tree nodes from a contiguous arena.
/// Indexing uses 32-bit integer handles instead of heap pointers.
/// Resetting the tree completes in O(1) time without deallocations.
#[derive(Clone, Debug)]
pub struct ArenaSoABTreeMap<K, V> {
    nodes: Vec<ArenaNode<K, V>>,
    root: u32,
    len: usize,
}

impl<K: InlineKey, V> Default for ArenaSoABTreeMap<K, V> {
    fn default() -> Self {
        Self::new()
    }
}

impl<K: InlineKey, V> ArenaSoABTreeMap<K, V> {
    pub fn new() -> Self {
        Self::with_capacity(4)
    }

    pub fn with_capacity(node_capacity: usize) -> Self {
        let mut nodes = Vec::with_capacity(node_capacity.max(1));
        nodes.push(ArenaNode::Leaf(ArenaLeafNode::new()));
        Self { nodes, root: 0, len: 0 }
    }

    #[inline]
    pub fn len(&self) -> usize {
        self.len
    }

    #[inline]
    pub fn is_empty(&self) -> bool {
        self.len == 0
    }

    #[inline]
    pub fn node_count(&self) -> usize {
        self.nodes.len()
    }

    /// Clears the tree in O(1) time while keeping allocated node memory.
    pub fn clear(&mut self) {
        self.nodes.clear();
        self.nodes.push(ArenaNode::Leaf(ArenaLeafNode::new()));
        self.root = 0;
        self.len = 0;
    }

    #[inline]
    pub fn get(&self, key: &K) -> Option<&V> {
        let prefix = key.inline_prefix();
        let mut curr = self.root;
        loop {
            match &self.nodes[curr as usize] {
                ArenaNode::Leaf(leaf) => match leaf.search(key, prefix) {
                    Ok(idx) => return leaf.values[idx].as_ref(),
                    Err(_) => return None,
                },
                ArenaNode::Internal(internal) => {
                    let child_idx = internal.search_child(key, prefix);
                    curr = internal.children[child_idx];
                }
            }
        }
    }

    #[inline]
    pub fn contains_key(&self, key: &K) -> bool {
        self.get(key).is_some()
    }

    pub fn insert(&mut self, key: K, value: V) -> Option<V> {
        let prefix = key.inline_prefix();
        let (replaced, split) = self.insert_into_node(self.root, key, value, prefix);
        if replaced.is_none() {
            self.len += 1;
        }

        if let Some((split_prefix, split_key, right_child)) = split {
            let mut new_root = ArenaInternalNode::new();
            new_root.prefixes[0] = split_prefix;
            new_root.keys[0] = Some(split_key);
            new_root.children[0] = self.root;
            new_root.children[1] = right_child;
            new_root.len = 1;
            let root_id = self.nodes.len() as u32;
            self.nodes.push(ArenaNode::Internal(new_root));
            self.root = root_id;
        }

        replaced
    }

    fn insert_into_node(
        &mut self,
        node_id: u32,
        key: K,
        value: V,
        prefix: u64,
    ) -> (Option<V>, Option<(u64, K, u32)>) {
        match &mut self.nodes[node_id as usize] {
            ArenaNode::Leaf(leaf) => match leaf.search(&key, prefix) {
                Ok(idx) => (leaf.values[idx].replace(value), None),
                Err(idx) => {
                    if leaf.len < SOA_NODE_CAPACITY {
                        for i in (idx..leaf.len).rev() {
                            leaf.prefixes[i + 1] = leaf.prefixes[i];
                            leaf.keys[i + 1] = leaf.keys[i].take();
                            leaf.values[i + 1] = leaf.values[i].take();
                        }
                        leaf.prefixes[idx] = prefix;
                        leaf.keys[idx] = Some(key);
                        leaf.values[idx] = Some(value);
                        leaf.len += 1;
                        (None, None)
                    } else {
                        let mid = SOA_NODE_CAPACITY / 2;
                        let mut right = ArenaLeafNode::new();
                        let mut r_len = 0;
                        for i in mid..SOA_NODE_CAPACITY {
                            right.prefixes[r_len] = leaf.prefixes[i];
                            right.keys[r_len] = leaf.keys[i].take();
                            right.values[r_len] = leaf.values[i].take();
                            r_len += 1;
                        }
                        right.len = r_len;
                        leaf.len = mid;

                        if idx < mid {
                            for i in (idx..leaf.len).rev() {
                                leaf.prefixes[i + 1] = leaf.prefixes[i];
                                leaf.keys[i + 1] = leaf.keys[i].take();
                                leaf.values[i + 1] = leaf.values[i].take();
                            }
                            leaf.prefixes[idx] = prefix;
                            leaf.keys[idx] = Some(key);
                            leaf.values[idx] = Some(value);
                            leaf.len += 1;
                        } else {
                            let r_idx = idx - mid;
                            for i in (r_idx..right.len).rev() {
                                right.prefixes[i + 1] = right.prefixes[i];
                                right.keys[i + 1] = right.keys[i].take();
                                right.values[i + 1] = right.values[i].take();
                            }
                            right.prefixes[r_idx] = prefix;
                            right.keys[r_idx] = Some(key);
                            right.values[r_idx] = Some(value);
                            right.len += 1;
                        }

                        let split_prefix = right.prefixes[0];
                        let split_key = right.keys[0].as_ref().unwrap().clone();
                        let right_id = self.nodes.len() as u32;
                        self.nodes.push(ArenaNode::Leaf(right));
                        (None, Some((split_prefix, split_key, right_id)))
                    }
                }
            },
            ArenaNode::Internal(internal) => {
                let child_idx = internal.search_child(&key, prefix);
                let child_id = internal.children[child_idx];
                let (replaced, split) = self.insert_into_node(child_id, key, value, prefix);

                if let Some((split_prefix, split_key, right_child)) = split {
                    let internal = match &mut self.nodes[node_id as usize] {
                        ArenaNode::Internal(node) => node,
                        _ => unreachable!(),
                    };

                    if internal.len < SOA_NODE_CAPACITY {
                        for i in (child_idx..internal.len).rev() {
                            internal.prefixes[i + 1] = internal.prefixes[i];
                            internal.keys[i + 1] = internal.keys[i].take();
                            internal.children[i + 2] = internal.children[i + 1];
                        }
                        internal.prefixes[child_idx] = split_prefix;
                        internal.keys[child_idx] = Some(split_key);
                        internal.children[child_idx + 1] = right_child;
                        internal.len += 1;
                        (replaced, None)
                    } else {
                        let mut all_prefixes = [0u64; SOA_NODE_CAPACITY + 1];
                        let mut all_keys: [Option<K>; SOA_NODE_CAPACITY + 1] = Default::default();
                        let mut all_children = [NULL_NODE; SOA_NODE_CAPACITY + 2];

                        for i in 0..child_idx {
                            all_prefixes[i] = internal.prefixes[i];
                            all_keys[i] = internal.keys[i].take();
                            all_children[i] = internal.children[i];
                        }
                        all_children[child_idx] = internal.children[child_idx];

                        all_prefixes[child_idx] = split_prefix;
                        all_keys[child_idx] = Some(split_key);
                        all_children[child_idx + 1] = right_child;

                        for i in child_idx..SOA_NODE_CAPACITY {
                            all_prefixes[i + 1] = internal.prefixes[i];
                            all_keys[i + 1] = internal.keys[i].take();
                            all_children[i + 2] = internal.children[i + 1];
                        }

                        let mid = SOA_NODE_CAPACITY / 2;
                        let promoted_prefix = all_prefixes[mid];
                        let promoted_key = all_keys[mid].take().unwrap();

                        for i in 0..mid {
                            internal.prefixes[i] = all_prefixes[i];
                            internal.keys[i] = all_keys[i].take();
                            internal.children[i] = all_children[i];
                        }
                        internal.children[mid] = all_children[mid];
                        internal.len = mid;

                        let mut right = ArenaInternalNode::new();
                        let mut r_idx = 0;
                        for i in (mid + 1)..=SOA_NODE_CAPACITY {
                            right.prefixes[r_idx] = all_prefixes[i];
                            right.keys[r_idx] = all_keys[i].take();
                            right.children[r_idx] = all_children[i];
                            r_idx += 1;
                        }
                        right.children[r_idx] = all_children[SOA_NODE_CAPACITY + 1];
                        right.len = r_idx;

                        let right_id = self.nodes.len() as u32;
                        self.nodes.push(ArenaNode::Internal(right));
                        (replaced, Some((promoted_prefix, promoted_key, right_id)))
                    }
                } else {
                    (replaced, None)
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_soa_btree_basic_insert_get() {
        let mut tree = SoABTreeMap::new();
        assert!(tree.is_empty());

        tree.insert("alpha".to_string(), 10u64);
        tree.insert("beta".to_string(), 20u64);
        tree.insert("gamma".to_string(), 30u64);

        assert_eq!(tree.len(), 3);
        assert_eq!(tree.get(&"alpha".to_string()), Some(&10));
        assert_eq!(tree.get(&"beta".to_string()), Some(&20));
        assert_eq!(tree.get(&"gamma".to_string()), Some(&30));
        assert_eq!(tree.get(&"delta".to_string()), None);
    }

    #[test]
    fn test_soa_btree_scale_splits() {
        let mut tree = SoABTreeMap::new();
        let total = 2_000;

        for i in 0..total {
            let key = format!("key_{:06}", i);
            tree.insert(key, i as u64);
        }

        assert_eq!(tree.len(), total);

        for i in 0..total {
            let key = format!("key_{:06}", i);
            assert_eq!(tree.get(&key), Some(&(i as u64)));
        }

        for i in total..total + 500 {
            let key = format!("key_{:06}", i);
            assert_eq!(tree.get(&key), None);
        }
    }

    #[test]
    fn test_soa_btree_numeric_keys() {
        let mut tree = SoABTreeMap::new();
        for i in 0..1_000 {
            tree.insert(i as u64, (i * 2) as u64);
        }

        for i in 0..1_000 {
            assert_eq!(tree.get(&(i as u64)), Some(&((i * 2) as u64)));
        }
    }

    #[test]
    fn test_arena_soa_btree_basic() {
        let mut tree = ArenaSoABTreeMap::new();
        assert!(tree.is_empty());
        assert_eq!(tree.len(), 0);

        tree.insert("one".to_string(), 1u64);
        tree.insert("two".to_string(), 2u64);
        tree.insert("three".to_string(), 3u64);

        assert_eq!(tree.len(), 3);
        assert_eq!(tree.get(&"one".to_string()), Some(&1));
        assert_eq!(tree.get(&"two".to_string()), Some(&2));
        assert_eq!(tree.get(&"three".to_string()), Some(&3));
        assert_eq!(tree.get(&"four".to_string()), None);

        // Replacement test
        let old = tree.insert("two".to_string(), 22u64);
        assert_eq!(old, Some(2));
        assert_eq!(tree.len(), 3);
        assert_eq!(tree.get(&"two".to_string()), Some(&22));
    }

    #[test]
    fn test_arena_soa_btree_scale_splits() {
        let mut tree = ArenaSoABTreeMap::with_capacity(256);
        let total = 2_000;

        for i in 0..total {
            let key = format!("k_{:06}", i);
            tree.insert(key, i as u64);
        }

        assert_eq!(tree.len(), total);

        for i in 0..total {
            let key = format!("k_{:06}", i);
            assert_eq!(tree.get(&key), Some(&(i as u64)));
        }

        for i in total..total + 200 {
            let key = format!("k_{:06}", i);
            assert_eq!(tree.get(&key), None);
        }
    }

    #[test]
    fn test_arena_soa_btree_numeric_and_reset() {
        let mut tree = ArenaSoABTreeMap::with_capacity(128);
        for i in 0..1_000 {
            tree.insert(i as u64, (i * 3) as u64);
        }
        assert_eq!(tree.len(), 1_000);
        let nodes_used = tree.node_count();
        assert!(nodes_used > 10);

        for i in 0..1_000 {
            assert_eq!(tree.get(&(i as u64)), Some(&((i * 3) as u64)));
        }

        // Test O(1) reset
        tree.clear();
        assert_eq!(tree.len(), 0);
        assert!(tree.is_empty());
        assert_eq!(tree.get(&500u64), None);

        // Reinsert after reset
        for i in 0..100 {
            tree.insert(i as u64, (i * 5) as u64);
        }
        assert_eq!(tree.len(), 100);
        for i in 0..100 {
            assert_eq!(tree.get(&(i as u64)), Some(&((i * 5) as u64)));
        }
    }
}
