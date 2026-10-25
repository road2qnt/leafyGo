-- ============================================================================
-- Outside Database Seed Script: outside_corp
-- Multi-schema (public + logistics), diverse datatypes, constraints, views,
-- and realistic sample data for testing leafyGo external DB connectivity.
-- ============================================================================

-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ----------------------------------------------------------------------------
-- Schemas
-- ----------------------------------------------------------------------------
CREATE SCHEMA IF NOT EXISTS logistics;

-- ----------------------------------------------------------------------------
-- Custom Types
-- ----------------------------------------------------------------------------
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'shipment_status') THEN
        CREATE TYPE shipment_status AS ENUM ('pending', 'in_transit', 'delivered', 'delayed', 'cancelled');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'supplier_tier') THEN
        CREATE TYPE supplier_tier AS ENUM ('bronze', 'silver', 'gold', 'platinum');
    END IF;
END $$;

-- ----------------------------------------------------------------------------
-- Table: public.suppliers
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.suppliers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    code VARCHAR(32) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    contact_email VARCHAR(255) NOT NULL,
    phone VARCHAR(64),
    country VARCHAR(64) NOT NULL DEFAULT 'Indonesia',
    tier supplier_tier NOT NULL DEFAULT 'bronze',
    rating NUMERIC(3, 2) NOT NULL DEFAULT 3.50 CHECK (rating >= 1.00 AND rating <= 5.00),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_suppliers_country ON public.suppliers (country);
CREATE INDEX IF NOT EXISTS idx_suppliers_tier ON public.suppliers (tier);

-- ----------------------------------------------------------------------------
-- Table: public.warehouses
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.warehouses (
    id SERIAL PRIMARY KEY,
    code VARCHAR(32) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    city VARCHAR(100) NOT NULL,
    country VARCHAR(100) NOT NULL DEFAULT 'Indonesia',
    capacity_sqm INTEGER NOT NULL CHECK (capacity_sqm > 0),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_warehouses_city ON public.warehouses (city);
CREATE INDEX IF NOT EXISTS idx_warehouses_metadata ON public.warehouses USING GIN (metadata);

-- ----------------------------------------------------------------------------
-- Table: public.products
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.products (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    sku VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    category VARCHAR(100) NOT NULL,
    supplier_id UUID REFERENCES public.suppliers(id) ON DELETE SET NULL,
    unit_price NUMERIC(12, 2) NOT NULL CHECK (unit_price >= 0),
    reorder_point INTEGER NOT NULL DEFAULT 10 CHECK (reorder_point >= 0),
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_products_category ON public.products (category);
CREATE INDEX IF NOT EXISTS idx_products_supplier_id ON public.products (supplier_id);
CREATE INDEX IF NOT EXISTS idx_products_attributes ON public.products USING GIN (attributes);

-- ----------------------------------------------------------------------------
-- Table: public.inventory_records (Composite Key)
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.inventory_records (
    warehouse_id INTEGER NOT NULL REFERENCES public.warehouses(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES public.products(id) ON DELETE CASCADE,
    quantity INTEGER NOT NULL DEFAULT 0 CHECK (quantity >= 0),
    aisle VARCHAR(16),
    bin VARCHAR(16),
    last_stocked_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (warehouse_id, product_id)
);

CREATE INDEX IF NOT EXISTS idx_inventory_records_qty ON public.inventory_records (quantity);

-- ----------------------------------------------------------------------------
-- Table: logistics.carriers
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS logistics.carriers (
    id SERIAL PRIMARY KEY,
    name VARCHAR(128) NOT NULL UNIQUE,
    code VARCHAR(32) NOT NULL UNIQUE,
    tracking_url_template VARCHAR(255),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ----------------------------------------------------------------------------
-- Table: logistics.shipments
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS logistics.shipments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tracking_number VARCHAR(64) NOT NULL UNIQUE,
    carrier_id INTEGER NOT NULL REFERENCES logistics.carriers(id) ON DELETE RESTRICT,
    origin_warehouse_id INTEGER REFERENCES public.warehouses(id) ON DELETE RESTRICT,
    destination_city VARCHAR(100) NOT NULL,
    destination_country VARCHAR(100) NOT NULL,
    status shipment_status NOT NULL DEFAULT 'pending',
    shipped_at TIMESTAMPTZ,
    estimated_delivery DATE,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_shipments_carrier ON logistics.shipments (carrier_id);
CREATE INDEX IF NOT EXISTS idx_shipments_status ON logistics.shipments (status);
CREATE INDEX IF NOT EXISTS idx_shipments_est_delivery ON logistics.shipments (estimated_delivery);

-- ----------------------------------------------------------------------------
-- Table: logistics.shipment_events
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS logistics.shipment_events (
    id BIGSERIAL PRIMARY KEY,
    shipment_id UUID NOT NULL REFERENCES logistics.shipments(id) ON DELETE CASCADE,
    status_update shipment_status NOT NULL,
    location VARCHAR(255) NOT NULL,
    notes TEXT,
    event_timestamp TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_shipment_events_shipment_id ON logistics.shipment_events (shipment_id);
CREATE INDEX IF NOT EXISTS idx_shipment_events_timestamp ON logistics.shipment_events (event_timestamp DESC);

-- ----------------------------------------------------------------------------
-- Views
-- ----------------------------------------------------------------------------
-- View 1: Low stock notification view
CREATE OR REPLACE VIEW public.v_low_stock_products AS
SELECT 
    p.id AS product_id,
    p.sku,
    p.name AS product_name,
    p.category,
    p.reorder_point,
    COALESCE(SUM(ir.quantity), 0)::BIGINT AS current_total_stock,
    (COALESCE(SUM(ir.quantity), 0) <= p.reorder_point) AS needs_reorder
FROM public.products p
LEFT JOIN public.inventory_records ir ON p.id = ir.product_id
GROUP BY p.id, p.sku, p.name, p.category, p.reorder_point;

-- View 2: Materialized View in logistics schema
DROP MATERIALIZED VIEW IF EXISTS logistics.mv_shipment_summary;
CREATE MATERIALIZED VIEW logistics.mv_shipment_summary AS
SELECT 
    c.id AS carrier_id,
    c.name AS carrier_name,
    s.status,
    COUNT(s.id) AS total_shipments,
    MIN(s.created_at) AS earliest_shipment,
    MAX(s.created_at) AS latest_shipment
FROM logistics.carriers c
LEFT JOIN logistics.shipments s ON c.id = s.carrier_id
GROUP BY c.id, c.name, s.status;

CREATE UNIQUE INDEX IF NOT EXISTS idx_mv_shipment_summary ON logistics.mv_shipment_summary (carrier_id, status);

-- ----------------------------------------------------------------------------
-- Sample Seed Data
-- ----------------------------------------------------------------------------

-- Suppliers
INSERT INTO public.suppliers (id, code, name, contact_email, phone, country, tier, rating)
VALUES 
    ('11111111-1111-4111-8111-111111111111', 'SUP-NEXUS', 'Nexus Microelectronics Ltd', 'procurement@nexus-micro.com', '+886-2-8765-4321', 'Taiwan', 'platinum', 4.95),
    ('22222222-2222-4222-8222-222222222222', 'SUP-JAVA-LOG', 'PT Java Logistik Nusantara', 'sales@javalog.co.id', '+62-21-555-0199', 'Indonesia', 'gold', 4.60),
    ('33333333-3333-4333-8333-333333333333', 'SUP-ALPINE', 'Alpine Precision Industrial GmbH', 'contact@alpine-precision.de', '+49-89-123456', 'Germany', 'platinum', 4.80),
    ('44444444-4444-4444-8444-444444444444', 'SUP-SUMMIT', 'Summit Packaging Co.', 'orders@summitpack.com', '+1-415-555-0142', 'United States', 'silver', 3.90),
    ('55555555-5555-4555-8555-555555555555', 'SUP-GLOBAL', 'Global Pacific Polymers', 'info@globalpacific.sg', '+65-6789-0123', 'Singapore', 'bronze', 3.40)
ON CONFLICT (code) DO NOTHING;

-- Warehouses
INSERT INTO public.warehouses (code, name, city, country, capacity_sqm, metadata)
VALUES 
    ('WH-JKT-01', 'Cengkareng Hub 1', 'Jakarta', 'Indonesia', 15000, '{"temp_controlled": true, "dock_doors": 16, "hazard_certified": false}'::jsonb),
    ('WH-SBY-02', 'Rungkut Distribution Center', 'Surabaya', 'Indonesia', 9500, '{"temp_controlled": false, "dock_doors": 8, "hazard_certified": true}'::jsonb),
    ('WH-SIN-01', 'Changi Logistics Facility', 'Singapore', 'Singapore', 22000, '{"temp_controlled": true, "dock_doors": 24, "hazard_certified": true}'::jsonb),
    ('WH-KUL-01', 'Shah Alam Regional Gateway', 'Kuala Lumpur', 'Malaysia', 12000, '{"temp_controlled": false, "dock_doors": 10, "hazard_certified": false}'::jsonb)
ON CONFLICT (code) DO NOTHING;

-- Products
INSERT INTO public.products (id, sku, name, category, supplier_id, unit_price, reorder_point, attributes)
VALUES 
    ('a0000000-0000-4000-8000-000000000001', 'PRD-SEMI-001', 'Cortex Microcontroller v4', 'Semiconductors', '11111111-1111-4111-8111-111111111111', 12.50, 100, '{"voltage": "3.3V", "architecture": "ARM-M4", "rohs": true}'::jsonb),
    ('a0000000-0000-4000-8000-000000000002', 'PRD-SEMI-002', 'Power Transistor Module 100A', 'Semiconductors', '11111111-1111-4111-8111-111111111111', 45.00, 40, '{"current": "100A", "voltage": "600V"}'::jsonb),
    ('a0000000-0000-4000-8000-000000000003', 'PRD-BEAR-101', 'High-Speed Ceramic Bearing 608', 'Mechanical', '33333333-3333-4333-8333-333333333333', 18.75, 50, '{"material": "Si3N4", "tolerance": "ABEC-7"}'::jsonb),
    ('a0000000-0000-4000-8000-000000000004', 'PRD-BEAR-202', 'Stainless Steel Ball Bearing 6204', 'Mechanical', '33333333-3333-4333-8333-333333333333', 9.20, 30, '{"grade": "AISI 440C"}'::jsonb),
    ('a0000000-0000-4000-8000-000000000005', 'PRD-POLY-010', 'Industrial Polymer Pellets 25kg', 'Raw Materials', '55555555-5555-4555-8555-555555555555', 85.00, 20, '{"type": "HDPE", "weight_kg": 25}'::jsonb),
    ('a0000000-0000-4000-8000-000000000006', 'PRD-BOX-050', 'Heavy Duty Corrugated Carton M', 'Packaging', '44444444-4444-4444-8444-444444444444', 2.30, 200, '{"burst_strength": "275 lb", "pack_size": 25}'::jsonb),
    ('a0000000-0000-4000-8000-000000000007', 'PRD-WRAP-100', 'Pallet Stretch Film 500mm', 'Packaging', '44444444-4444-4444-8444-444444444444', 16.00, 50, '{"thickness_micron": 23, "length_m": 300}'::jsonb),
    ('a0000000-0000-4000-8000-000000000008', 'PRD-SEMI-003', 'Optoisolator IC Quad-Channel', 'Semiconductors', '11111111-1111-4111-8111-111111111111', 4.15, 150, '{"isolation_voltage": "5000Vrms"}'::jsonb)
ON CONFLICT (sku) DO NOTHING;

-- Inventory Records
INSERT INTO public.inventory_records (warehouse_id, product_id, quantity, aisle, bin)
VALUES 
    (1, 'a0000000-0000-4000-8000-000000000001', 250, 'A-01', 'B-12'),
    (1, 'a0000000-0000-4000-8000-000000000002', 15,  'A-02', 'B-04'), -- Low stock
    (1, 'a0000000-0000-4000-8000-000000000006', 850, 'C-05', 'D-01'),
    (1, 'a0000000-0000-4000-8000-000000000007', 120, 'C-06', 'D-02'),
    (2, 'a0000000-0000-4000-8000-000000000003', 42,  'M-01', 'T-03'), -- Low stock
    (2, 'a0000000-0000-4000-8000-000000000004', 180, 'M-02', 'T-05'),
    (2, 'a0000000-0000-4000-8000-000000000005', 65,  'R-01', 'B-01'),
    (3, 'a0000000-0000-4000-8000-000000000001', 500, 'SG-A', '01-01'),
    (3, 'a0000000-0000-4000-8000-000000000002', 80,  'SG-A', '01-02'),
    (3, 'a0000000-0000-4000-8000-000000000008', 340, 'SG-B', '03-10'),
    (4, 'a0000000-0000-4000-8000-000000000003', 150, 'KL-1', 'A-01'),
    (4, 'a0000000-0000-4000-8000-000000000005', 10,  'KL-2', 'A-08')  -- Low stock
ON CONFLICT (warehouse_id, product_id) DO UPDATE 
SET quantity = EXCLUDED.quantity, last_stocked_at = CURRENT_TIMESTAMP;

-- Logistics Carriers
INSERT INTO logistics.carriers (id, name, code, tracking_url_template)
VALUES 
    (1, 'DHL Express Global', 'DHL', 'https://www.dhl.com/en/express/tracking.html?AWB={tracking}'),
    (2, 'FedEx International', 'FEDEX', 'https://www.fedex.com/fedextrack/?trknbr={tracking}'),
    (3, 'PT JNE Express', 'JNE', 'https://www.jne.co.id/tracking/{tracking}'),
    (4, 'UPS Worldwide', 'UPS', 'https://www.ups.com/track?tracknum={tracking}')
ON CONFLICT (code) DO NOTHING;

-- Logistics Shipments
INSERT INTO logistics.shipments (id, tracking_number, carrier_id, origin_warehouse_id, destination_city, destination_country, status, shipped_at, estimated_delivery, delivered_at)
VALUES 
    ('10000000-0000-0000-0000-000000000001', 'DHL-8492019482', 1, 3, 'Munich', 'Germany', 'delivered', CURRENT_TIMESTAMP - INTERVAL '7 days', (CURRENT_DATE - 3), CURRENT_TIMESTAMP - INTERVAL '3 days'),
    ('10000000-0000-0000-0000-000000000002', 'DHL-8492019483', 1, 1, 'Tokyo', 'Japan', 'in_transit', CURRENT_TIMESTAMP - INTERVAL '2 days', (CURRENT_DATE + 2), NULL),
    ('10000000-0000-0000-0000-000000000003', 'FDX-9920104820', 2, 3, 'San Francisco', 'United States', 'in_transit', CURRENT_TIMESTAMP - INTERVAL '1 day', (CURRENT_DATE + 3), NULL),
    ('10000000-0000-0000-0000-000000000004', 'FDX-9920104821', 2, 2, 'Sydney', 'Australia', 'delayed', CURRENT_TIMESTAMP - INTERVAL '4 days', (CURRENT_DATE + 1), NULL),
    ('10000000-0000-0000-0000-000000000005', 'JNE-0129384756', 3, 1, 'Bandung', 'Indonesia', 'delivered', CURRENT_TIMESTAMP - INTERVAL '3 days', (CURRENT_DATE - 1), CURRENT_TIMESTAMP - INTERVAL '1 day'),
    ('10000000-0000-0000-0000-000000000006', 'JNE-0129384757', 3, 1, 'Medan', 'Indonesia', 'in_transit', CURRENT_TIMESTAMP - INTERVAL '12 hours', (CURRENT_DATE + 1), NULL),
    ('10000000-0000-0000-0000-000000000007', 'UPS-1Z99999999', 4, 4, 'London', 'United Kingdom', 'pending', NULL, (CURRENT_DATE + 5), NULL),
    ('10000000-0000-0000-0000-000000000008', 'UPS-1Z88888888', 4, 1, 'Rotterdam', 'Netherlands', 'cancelled', NULL, NULL, NULL)
ON CONFLICT (tracking_number) DO NOTHING;

-- Logistics Shipment Events
INSERT INTO logistics.shipment_events (shipment_id, status_update, location, notes, event_timestamp)
VALUES 
    ('10000000-0000-0000-0000-000000000001', 'pending', 'Singapore Changi WH', 'Parcel created & packaged', CURRENT_TIMESTAMP - INTERVAL '7 days'),
    ('10000000-0000-0000-0000-000000000001', 'in_transit', 'SIN Airport Freight Center', 'Departed origin airport', CURRENT_TIMESTAMP - INTERVAL '6 days'),
    ('10000000-0000-0000-0000-000000000001', 'in_transit', 'Frankfurt Hub', 'Customs cleared', CURRENT_TIMESTAMP - INTERVAL '4 days'),
    ('10000000-0000-0000-0000-000000000001', 'delivered', 'Munich Delivery Depot', 'Signed by Schmidt', CURRENT_TIMESTAMP - INTERVAL '3 days'),
    ('10000000-0000-0000-0000-000000000002', 'pending', 'Jakarta Cengkareng WH', 'Documentation approved', CURRENT_TIMESTAMP - INTERVAL '2 days'),
    ('10000000-0000-0000-0000-000000000002', 'in_transit', 'CGK Air Hub', 'Loaded onto flight NH836', CURRENT_TIMESTAMP - INTERVAL '1 day'),
    ('10000000-0000-0000-0000-000000000004', 'delayed', 'Surabaya Port', 'Vessel clearance pending typhoon alert', CURRENT_TIMESTAMP - INTERVAL '2 days'),
    ('10000000-0000-0000-0000-000000000005', 'delivered', 'Bandung Office', 'Delivered to reception desk', CURRENT_TIMESTAMP - INTERVAL '1 day');

-- Refresh materialized view
REFRESH MATERIALIZED VIEW logistics.mv_shipment_summary;

-- ----------------------------------------------------------------------------
-- Permissions & Roles
-- Ensure outside_admin has full rights on both schemas
-- ----------------------------------------------------------------------------
GRANT ALL ON SCHEMA public TO outside_admin;
GRANT ALL ON SCHEMA logistics TO outside_admin;
GRANT ALL ON ALL TABLES IN SCHEMA public TO outside_admin;
GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO outside_admin;
GRANT ALL ON ALL TABLES IN SCHEMA logistics TO outside_admin;
GRANT ALL ON ALL SEQUENCES IN SCHEMA logistics TO outside_admin;
