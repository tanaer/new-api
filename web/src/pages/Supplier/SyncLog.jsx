import React, { useState, useEffect, useCallback } from 'react';
import {
    Table,
    Tag,
    Select,
    Space,
    Typography,
    Button,
} from '@douyinfe/semi-ui';
import { IconRefresh } from '@douyinfe/semi-icons';

const { Text, Paragraph } = Typography;

async function apiFetch(url, options = {}) {
    const res = await fetch(url, {
        ...options,
        headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${localStorage.getItem('token')}`,
            ...options.headers,
        },
    });
    return res.json();
}

const SyncLogPage = () => {
    const [logs, setLogs] = useState([]);
    const [total, setTotal] = useState(0);
    const [loading, setLoading] = useState(false);
    const [page, setPage] = useState(1);
    const [pageSize] = useState(20);
    const [suppliers, setSuppliers] = useState([]);
    const [filterSupplier, setFilterSupplier] = useState(0);

    const loadLogs = useCallback(async () => {
        setLoading(true);
        try {
            const params = new URLSearchParams({
                p: String(page - 1),
                page_size: String(pageSize),
            });
            if (filterSupplier > 0) params.set('supplier_id', String(filterSupplier));
            const data = await apiFetch(`/api/supplier/sync_logs?${params}`);
            if (data.success) {
                setLogs(data.data || []);
                setTotal(data.total || 0);
            }
        } finally {
            setLoading(false);
        }
    }, [page, pageSize, filterSupplier]);

    const loadSuppliers = useCallback(async () => {
        const data = await apiFetch('/api/supplier/');
        if (data.success) setSuppliers(data.data || []);
    }, []);

    useEffect(() => { loadSuppliers(); }, [loadSuppliers]);
    useEffect(() => { loadLogs(); }, [loadLogs]);

    const syncTypeMap = {
        ratio_change: { text: '倍率变动', color: 'orange' },
        group_added: { text: '新增分组', color: 'green' },
        group_removed: { text: '移除分组', color: 'red' },
        group_changed: { text: '分组变动', color: 'blue' },
    };

    const columns = [
        { title: 'ID', dataIndex: 'id', width: 60 },
        { title: '供应商', dataIndex: 'supplier_name', width: 120 },
        {
            title: '类型',
            dataIndex: 'sync_type',
            width: 100,
            render: (type) => {
                const info = syncTypeMap[type] || { text: type, color: 'grey' };
                return <Tag color={info.color}>{info.text}</Tag>;
            },
        },
        {
            title: '详情',
            dataIndex: 'details',
            render: (text) => {
                try {
                    const details = JSON.parse(text);
                    return (
                        <Paragraph ellipsis={{ rows: 2, expandable: true }} style={{ maxWidth: 500, margin: 0 }}>
                            {JSON.stringify(details, null, 2)}
                        </Paragraph>
                    );
                } catch {
                    return <Text>{text}</Text>;
                }
            },
        },
        {
            title: '时间',
            dataIndex: 'created_time',
            width: 180,
            render: (ts) => ts ? new Date(ts * 1000).toLocaleString() : '-',
        },
    ];

    return (
        <div className='mt-[60px] px-2'>
            <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <Space>
                    <Select
                        placeholder='筛选供应商'
                        value={filterSupplier}
                        onChange={setFilterSupplier}
                        style={{ width: 200 }}
                        optionList={[
                            { label: '全部', value: 0 },
                            ...suppliers.map((s) => ({ label: s.name, value: s.id })),
                        ]}
                    />
                    <Button icon={<IconRefresh />} onClick={loadLogs}>
                        刷新
                    </Button>
                </Space>
            </div>

            <Table
                columns={columns}
                dataSource={logs}
                rowKey='id'
                loading={loading}
                pagination={{
                    currentPage: page,
                    pageSize,
                    total,
                    onPageChange: setPage,
                }}
            />
        </div>
    );
};

export default SyncLogPage;
