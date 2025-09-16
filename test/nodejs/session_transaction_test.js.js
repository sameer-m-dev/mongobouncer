const mongoose = require('mongoose');

const MONGODB_BOUNCER_URI = 'mongodb://localhost:27017/analytics_rs6?retryWrites=true&retryReads=true';

async function debugTest() {
    console.log('🔗 Connecting to MongoBouncer...');

    try {
        await mongoose.connect(MONGODB_BOUNCER_URI);
        console.log('✅ Connected successfully');

        // Test 1: Simple operation without session
        console.log('\n🧪 Test 1: Simple operation without session');
        const UserSchema = new mongoose.Schema({
            name: String,
            email: String
        });
        const User = mongoose.model('User', UserSchema);

        const user = new User({ name: 'TestUser', email: 'test@example.com' });
        await user.save();
        console.log('✅ User created successfully');

        // Test 2: Simple session without transaction
        console.log('\n🧪 Test 2: Simple session without transaction');
        const session = await mongoose.startSession();
        console.log('✅ Session started');

        const user2 = new User({ name: 'SessionUser', email: 'session@example.com' });
        await user2.save({ session });
        console.log('✅ User created with session');

        await session.endSession();
        console.log('✅ Session ended');

        // Test 3: Simple transaction
        console.log('\n🧪 Test 3: Simple transaction');
        const session2 = await mongoose.startSession();
        await session2.startTransaction();
        console.log('✅ Transaction started');

        const user3 = new User({ name: 'TxnUser', email: 'txn@example.com' });
        await user3.save({ session: session2 });
        console.log('✅ User created in transaction');

        await session2.commitTransaction();
        console.log('✅ Transaction committed');

        await session2.endSession();
        console.log('✅ Session ended');

        console.log('\n🎉 All tests passed!');

    } catch (error) {
        console.error('❌ Test failed:', error.message);
        console.error('Stack:', error.stack);
    } finally {
        await mongoose.disconnect();
        console.log('🔌 Disconnected');
    }
}

debugTest().catch(console.error);
